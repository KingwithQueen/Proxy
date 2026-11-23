/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "core.h"

#include <fcntl.h>
#include <io.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "mbedtls/sha256.h"

enum { MAX_CONFIG = 65536, MAX_PASSWORD = 16384 };

typedef struct {
    const unsigned char *next, *end;
} Json;

static void skip_space(Json *json)
{
    while (json->next < json->end &&
           (*json->next == ' ' || *json->next == '\r' ||
            *json->next == '\n' || *json->next == '\t'))
        ++json->next;
}

static bool take(Json *json, unsigned char byte)
{
    skip_space(json);
    if (json->next == json->end || *json->next != byte)
        return false;
    ++json->next;
    return true;
}

static bool literal(Json *json, const char *value)
{
    size_t length = strlen(value);
    skip_space(json);
    if ((size_t)(json->end - json->next) < length || memcmp(json->next, value, length))
        return false;
    json->next += length;
    return true;
}

static int hex_value(unsigned char c)
{
    if (c >= '0' && c <= '9')
        return c - '0';
    if (c >= 'a' && c <= 'f')
        return c - 'a' + 10;
    if (c >= 'A' && c <= 'F')
        return c - 'A' + 10;
    return -1;
}

static bool unicode_escape(Json *json, uint32_t *value)
{
    int digit;
    if ((size_t)(json->end - json->next) < 4)
        return false;
    *value = 0;
    for (size_t i = 0; i < 4; ++i) {
        digit = hex_value(*json->next++);
        if (digit < 0)
            return false;
        *value = (*value << 4) | (uint32_t)digit;
    }
    return true;
}

static size_t write_utf8(uint32_t code, unsigned char out[4])
{
    if (code < 0x80) {
        out[0] = (unsigned char)code;
        return 1;
    }
    if (code < 0x800) {
        out[0] = (unsigned char)(0xc0 | (code >> 6));
        out[1] = (unsigned char)(0x80 | (code & 0x3f));
        return 2;
    }
    if (code < 0x10000) {
        out[0] = (unsigned char)(0xe0 | (code >> 12));
        out[1] = (unsigned char)(0x80 | ((code >> 6) & 0x3f));
        out[2] = (unsigned char)(0x80 | (code & 0x3f));
        return 3;
    }
    out[0] = (unsigned char)(0xf0 | (code >> 18));
    out[1] = (unsigned char)(0x80 | ((code >> 12) & 0x3f));
    out[2] = (unsigned char)(0x80 | ((code >> 6) & 0x3f));
    out[3] = (unsigned char)(0x80 | (code & 0x3f));
    return 4;
}

static bool string_escape(Json *json, unsigned char out[4], size_t *size)
{
    uint32_t code, second;
    if (json->next == json->end)
        return false;
    *size = 1;
    switch (*json->next++) {
    case '"': out[0] = '"'; return true;
    case '\\': out[0] = '\\'; return true;
    case '/': out[0] = '/'; return true;
    case 'b': out[0] = '\b'; return true;
    case 'f': out[0] = '\f'; return true;
    case 'n': out[0] = '\n'; return true;
    case 'r': out[0] = '\r'; return true;
    case 't': out[0] = '\t'; return true;
    case 'u':
        if (!unicode_escape(json, &code) || (code >= 0xdc00 && code <= 0xdfff))
            return false;
        if (code >= 0xd800 && code <= 0xdbff) {
            if ((size_t)(json->end - json->next) < 2 ||
                json->next[0] != '\\' || json->next[1] != 'u')
                return false;
            json->next += 2;
            if (!unicode_escape(json, &second) || second < 0xdc00 || second > 0xdfff)
                return false;
            code = 0x10000 + ((code - 0xd800) << 10) + (second - 0xdc00);
        }
        *size = write_utf8(code, out);
        return true;
    default:
        return false;
    }
}

static bool json_string(Json *json, char *out, size_t capacity, size_t *out_length)
{
    size_t used = 0;
    if (!take(json, '"'))
        return false;
    while (json->next < json->end) {
        unsigned char bytes[4] = { *json->next++ };
        size_t count = 1;
        if (bytes[0] == '"') {
            out[used] = '\0';
            if (out_length) {
                *out_length = used;
                return true;
            }
            return memchr(out, '\0', used) == NULL;
        }
        if (bytes[0] < 0x20)
            return false;
        if (bytes[0] == '\\' && !string_escape(json, bytes, &count))
            return false;
        if (count >= capacity - used)
            return false;
        memcpy(out + used, bytes, count);
        used += count;
    }
    return false;
}

static bool json_port(Json *json, unsigned short *port)
{
    unsigned int value = 0;
    skip_space(json);
    if (json->next == json->end || *json->next < '0' || *json->next > '9')
        return false;
    if (*json->next == '0') {
        ++json->next;
    } else {
        do {
            value = value * 10 + *json->next++ - '0';
            if (value > 65535)
                return false;
        } while (json->next < json->end && *json->next >= '0' && *json->next <= '9');
    }
    *port = (unsigned short)value;
    return true;
}

static bool json_bool(Json *json, bool *value)
{
    if (literal(json, "true"))
        *value = true;
    else if (literal(json, "false"))
        *value = false;
    else
        return false;
    return true;
}

bool normalize_host(char host[MAX_HOST + 1])
{
    unsigned char address[16];
    size_t label = 0;
    size_t len = strlen(host);
    if (len >= 2 && host[0] == '[' && host[len - 1] == ']') {
        len -= 2;
        memmove(host, host + 1, len);
        host[len] = '\0';
        return InetPtonA(AF_INET6, host, address) == 1;
    }
    if (InetPtonA(AF_INET6, host, address) == 1)
        return true;
    if (len && host[len - 1] == '.')
        host[--len] = '\0';
    if (!len || len > 253)
        return false;
    for (size_t i = 0; i < len; ++i) {
        char c = host[i];
        if (c == '.') {
            if (!label)
                return false;
            label = 0;
        } else if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
                   (c >= '0' && c <= '9') || c == '-' || c == '_') {
            if (++label > 63)
                return false;
        } else {
            return false;
        }
    }
    return label != 0;
}

static bool json_alpn(Json *json, Settings *settings)
{
    size_t count = 0;
    if (!take(json, '['))
        return false;
    if (take(json, ']'))
        return true;
    do {
        if (count == MAX_ALPN ||
            !json_string(json, settings->alpn_storage[count], 256, NULL) ||
            !settings->alpn_storage[count][0])
            return false;
        settings->alpn[count] = settings->alpn_storage[count];
        ++count;
    } while (take(json, ','));
    return take(json, ']');
}

static bool json_password(Json *json, Settings *settings, bool hashed)
{
    static const char hex[] = "0123456789abcdef";
    unsigned char digest[32];
    char password[MAX_PASSWORD + 1];
    size_t used;
    bool valid = false;
    if (hashed) {
        if (!json_string(json, settings->password_hash, sizeof(settings->password_hash),
                         &used) || used != AUTH_HEX_SIZE)
            return false;
        for (size_t i = 0; i < used; ++i) {
            int digit = hex_value((unsigned char)settings->password_hash[i]);
            if (digit < 0)
                return false;
            settings->password_hash[i] = hex[digit];
        }
        return true;
    }
    if (json_string(json, password, sizeof(password), &used) && used &&
        mbedtls_sha256((const unsigned char *)password, used, digest, 1) == 0) {
        for (size_t i = 0; i < AUTH_HEX_SIZE / 2; ++i) {
            settings->password_hash[i * 2] = hex[digest[i] >> 4];
            settings->password_hash[i * 2 + 1] = hex[digest[i] & 15];
        }
        valid = true;
    }
    SecureZeroMemory(password, sizeof(password));
    SecureZeroMemory(digest, sizeof(digest));
    return valid;
}

static bool json_protocol(Json *json, Settings *settings)
{
    char value[16];
    if (!json_string(json, value, sizeof(value), NULL))
        return false;
    if (!strcmp(value, "trojan"))
        settings->protocol = OUTBOUND_TROJAN;
    else if (!strcmp(value, "vmess"))
        settings->protocol = OUTBOUND_VMESS;
    else
        return false;
    return true;
}

static bool json_vmess_uuid(Json *json, unsigned char uuid[16])
{
    char text[37];
    size_t position = 0;
    bool valid = false;
    if (!json_string(json, text, sizeof(text), NULL) || strlen(text) != 36)
        goto cleanup;

    /* 按 UUID 的文本顺序读入字节，避免 Windows GUID 字段的字节序差异。 */
    for (size_t i = 0; i < 16; ++i) {
        if ((i == 4 || i == 6 || i == 8 || i == 10) && text[position++] != '-')
            goto cleanup;
        int high = hex_value((unsigned char)text[position++]);
        int low = hex_value((unsigned char)text[position++]);
        if (high < 0 || low < 0)
            goto cleanup;
        uuid[i] = (unsigned char)((high << 4) | low);
    }
    valid = true;

cleanup:
    SecureZeroMemory(text, sizeof(text));
    return valid;
}

static bool json_vmess_security(Json *json, VmessSecurity *security)
{
    char value[32];
    if (!json_string(json, value, sizeof(value), NULL))
        return false;
    if (!strcmp(value, "auto") || !strcmp(value, "aes-128-gcm"))
        *security = VMESS_SECURITY_AES_128_GCM;
    else if (!strcmp(value, "chacha20-poly1305"))
        *security = VMESS_SECURITY_CHACHA20_POLY1305;
    else if (!strcmp(value, "none"))
        *security = VMESS_SECURITY_NONE;
    else if (!strcmp(value, "zero"))
        *security = VMESS_SECURITY_ZERO;
    else
        return false;
    return true;
}

static bool parse_settings(Settings *settings, Json *json)
{
    enum { SERVER, SERVER_PORT, PASSWORD, PASSWORD_HASH, SNI, VERIFY,
           SOCKS, HTTP, ALPN, CA, PROTOCOL, UUID, ALTER_ID, SECURITY, TLS, NUM_FIELDS };
    static const char *const fields[] = {
        "server", "server_port", "password", "password_sha224", "sni", "verify_tls",
        "socks_port", "http_port", "alpn", "ca_file", "protocol", "uuid", "alter_id",
        "security", "tls"
    };
    unsigned int present = 0;
    if (!take(json, '{'))
        return false;
    do {
        char key[32];
        unsigned int field;
        bool value = false;
        if (!json_string(json, key, sizeof(key), NULL) || !take(json, ':'))
            return false;
        for (field = 0; field < NUM_FIELDS && strcmp(fields[field], key); ++field)
            ;
        if (field == NUM_FIELDS || (present & (1u << field)))
            return false;
        present |= 1u << field;
        switch (field) {
        case SERVER:
            value = json_string(json, settings->server, sizeof(settings->server), NULL);
            break;
        case SERVER_PORT:
            value = json_port(json, &settings->server_port);
            break;
        case PASSWORD:
        case PASSWORD_HASH:
            value = json_password(json, settings, field == PASSWORD_HASH);
            break;
        case SNI:
            value = json_string(json, settings->sni, sizeof(settings->sni), NULL);
            break;
        case VERIFY:
            value = json_bool(json, &settings->verify_tls);
            break;
        case SOCKS:
            value = json_port(json, &settings->socks_port);
            break;
        case HTTP:
            value = json_port(json, &settings->http_port);
            break;
        case ALPN:
            value = json_alpn(json, settings);
            break;
        case CA: {
            char path[4 * 1024];
            value = json_string(json, path, sizeof(path), NULL) &&
                MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1,
                                    settings->ca_file, 1024) > 0;
            break;
        }
        case PROTOCOL:
            value = json_protocol(json, settings);
            break;
        case UUID:
            value = json_vmess_uuid(json, settings->vmess_uuid);
            break;
        case ALTER_ID:
            /* VMess AEAD 使用主 UUID，兼容配置中的 alter_id 固定为 0。 */
            value = literal(json, "0");
            break;
        case SECURITY:
            value = json_vmess_security(json, &settings->vmess_security);
            break;
        case TLS:
            value = json_bool(json, &settings->use_tls);
            break;
        }
        if (!value) {
            fprintf(stderr, "Cray: invalid config setting %s\n", fields[field]);
            return false;
        }
    } while (take(json, ','));
    if (!take(json, '}'))
        return false;
    skip_space(json);
    if (json->next != json->end || !normalize_host(settings->server) ||
        !settings->server_port || settings->socks_port == settings->http_port ||
        (settings->sni[0] && !normalize_host(settings->sni)))
        return false;

    const unsigned int password_fields = (1u << PASSWORD) | (1u << PASSWORD_HASH);
    if (!(present & (1u << TLS)))
        settings->use_tls = settings->protocol == OUTBOUND_TROJAN;
    if (settings->protocol == OUTBOUND_VMESS) {
        if (!(present & (1u << UUID)) || (present & password_fields)) {
            fprintf(stderr, "Cray: use only uuid to authenticate VMess\n");
            return false;
        }
    } else {
        const unsigned int vmess_fields = (1u << UUID) | (1u << ALTER_ID) | (1u << SECURITY);
        if (present & vmess_fields) {
            fprintf(stderr, "Cray: uuid, alter_id and security require protocol vmess\n");
            return false;
        }
        if (!settings->use_tls || !settings->password_hash[0] ||
            (present & password_fields) == password_fields) {
            fprintf(stderr, "Cray: Trojan requires TLS and exactly one password field\n");
            return false;
        }
    }
    return true;
}

bool read_settings(Settings *settings, const wchar_t *file)
{
    bool valid = false;
    bool from_stdin = wcscmp(file, L"-") == 0;
    FILE *stream = from_stdin ? stdin : _wfopen(file, L"rb");
    unsigned char *text = NULL;
    if (!stream) {
        fprintf(stderr, "Cray: could not open config\n");
        return false;
    }
    if (from_stdin && _setmode(_fileno(stdin), _O_BINARY) == -1)
        return false;
    text = malloc(MAX_CONFIG + 1);
    if (text) {
        size_t length = fread(text, 1, MAX_CONFIG + 1, stream);
        if (length && length <= MAX_CONFIG && !ferror(stream) &&
            MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (const char *)text,
                                (int)length, NULL, 0) > 0) {
            Json json = { text, text + length };
            if (length >= 3 && !memcmp(text, "\xef\xbb\xbf", 3))
                json.next += 3;
            settings->server_port = 443;
            settings->socks_port = 10808;
            settings->http_port = 10809;
            settings->verify_tls = true;
            settings->protocol = OUTBOUND_TROJAN;
            settings->vmess_security = VMESS_SECURITY_AES_128_GCM;
            valid = parse_settings(settings, &json);
            if (valid && settings->use_tls && !settings->sni[0])
                memcpy(settings->sni, settings->server, strlen(settings->server) + 1);
        }
        SecureZeroMemory(text, MAX_CONFIG + 1);
        free(text);
    }
    if (!from_stdin)
        fclose(stream);
    if (!valid)
        fprintf(stderr, "Cray: invalid config (expected UTF-8 JSON and distinct listener ports)\n");
    return valid;
}
