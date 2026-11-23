/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "core.h"

#include <limits.h>
#include <stdio.h>
#include <string.h>

typedef struct {
    char *name, *value;
} Header;

/* VMess 和 VLESS 使用同一地址格式，端口在地址类型之前。 */
size_t write_v2ray_address(unsigned char output[V2RAY_ADDRESS_SIZE], const Destination *target)
{
    output[0] = (unsigned char)(target->port >> 8);
    output[1] = (unsigned char)target->port;
    if (InetPtonA(AF_INET, target->host, output + 3) == 1) {
        output[2] = 1;
        return 7;
    }
    if (InetPtonA(AF_INET6, target->host, output + 3) == 1) {
        output[2] = 3;
        return 19;
    }
    size_t length = strlen(target->host);
    if (!length || length > MAX_HOST)
        return 0;
    output[2] = 2;
    output[3] = (unsigned char)length;
    memcpy(output + 4, target->host, length);
    return 4 + length;
}

bool set_nonblocking(SOCKET socket)
{
    u_long mode = 1;
    return ioctlsocket(socket, (long)FIONBIO, &mode) == 0;
}

int wait_socket(SOCKET socket, bool read, ULONGLONG deadline)
{
    ULONGLONG now = GetTickCount64();
    if (now >= deadline)
        return 0;
    ULONGLONG remaining = deadline - now;
    struct timeval timeout = { (long)(remaining / 1000), (long)((remaining % 1000) * 1000) };
    fd_set requested, errors;
    FD_ZERO(&requested);
    FD_ZERO(&errors);
    FD_SET(socket, &requested);
    FD_SET(socket, &errors);
    int result = select(0, read ? &requested : NULL, read ? NULL : &requested,
                        &errors, &timeout);
    return result > 0 && FD_ISSET(socket, &errors) ? -1 : result;
}

static int read_local(SOCKET socket, unsigned char *buffer, size_t size, ULONGLONG deadline)
{
    while (GetTickCount64() < deadline) {
        int count = recv(socket, (char *)buffer, (int)size, 0);
        if (count >= 0)
            return count;
        if (WSAGetLastError() != WSAEWOULDBLOCK || wait_socket(socket, true, deadline) <= 0)
            break;
    }
    return -1;
}

static bool read_exact(SOCKET socket, unsigned char *buffer, size_t size, ULONGLONG deadline)
{
    while (size) {
        int count = read_local(socket, buffer, size, deadline);
        if (count <= 0)
            return false;
        buffer += count;
        size -= (size_t)count;
    }
    return true;
}

bool send_local(SOCKET socket, const void *data, size_t size, ULONGLONG deadline)
{
    const char *buffer = data;
    while (size && GetTickCount64() < deadline) {
        int count = send(socket, buffer, (int)size, 0);
        if (count > 0) {
            buffer += count;
            size -= (size_t)count;
        } else if (count == 0 || WSAGetLastError() != WSAEWOULDBLOCK ||
                   wait_socket(socket, false, deadline) <= 0) {
            return false;
        }
    }
    return size == 0;
}

static bool socks_reply(SOCKET socket, unsigned char code, ULONGLONG deadline)
{
    const unsigned char reply[] = { 5, code, 0, 1, 0, 0, 0, 0, 0, 0 };
    return send_local(socket, reply, sizeof(reply), deadline);
}

static bool socks_request(SOCKET socket, LocalRequest *request, ULONGLONG deadline)
{
    unsigned char packet[255];
    unsigned char method[] = { 5, 0xff };
    if (!read_exact(socket, packet, 2, deadline) || packet[0] != 5 || !packet[1])
        return false;
    unsigned char methods = packet[1];
    if (!read_exact(socket, packet, methods, deadline))
        return false;
    if (memchr(packet, 0, methods))
        method[1] = 0;
    if (!send_local(socket, method, sizeof(method), deadline) || method[1] != 0)
        return false;
    if (!read_exact(socket, packet, 4, deadline) || packet[0] != 5 || packet[2] != 0)
        return false;
    if (packet[1] != 1) {
        socks_reply(socket, 7, deadline);
        return false;
    }
    switch (packet[3]) {
    case 1:
        if (!read_exact(socket, packet, 4, deadline) ||
            !InetNtopA(AF_INET, packet, request->target.host, sizeof(request->target.host)))
            return false;
        break;
    case 4:
        if (!read_exact(socket, packet, 16, deadline) ||
            !InetNtopA(AF_INET6, packet, request->target.host, sizeof(request->target.host)))
            return false;
        break;
    case 3: {
        unsigned char size;
        if (!read_exact(socket, &size, 1, deadline) || !size ||
            !read_exact(socket, (unsigned char *)request->target.host, size, deadline))
            return false;
        request->target.host[size] = '\0';
        if (memchr(request->target.host, '\0', size) || !normalize_host(request->target.host)) {
            socks_reply(socket, 8, deadline);
            return false;
        }
        break;
    }
    default:
        socks_reply(socket, 8, deadline);
        return false;
    }
    if (!read_exact(socket, packet, 2, deadline))
        return false;
    request->target.port = (unsigned short)((packet[0] << 8) | packet[1]);
    if (!request->target.port) {
        socks_reply(socket, 1, deadline);
        return false;
    }
    request->protocol = LOCAL_SOCKS;
    return true;
}

static bool http_error(SOCKET socket, const char *status, ULONGLONG deadline)
{
    char response[128];
    int size = snprintf(response, sizeof(response),
        "HTTP/1.1 %s\r\nConnection: close\r\nContent-Length: 0\r\n\r\n", status);
    return size > 0 && (size_t)size < sizeof(response) &&
           send_local(socket, response, (size_t)size, deadline);
}

bool reply_local(SOCKET socket, LocalProtocol protocol, bool success)
{
    ULONGLONG deadline = GetTickCount64() + HANDSHAKE_TIMEOUT;
    static const char connected[] = "HTTP/1.1 200 Connection Established\r\n\r\n";
    if (protocol == LOCAL_SOCKS)
        return socks_reply(socket, success ? 0 : 1, deadline);
    if (!success)
        return http_error(socket, "502 Bad Gateway", deadline);
    return protocol != LOCAL_HTTP_CONNECT ||
           send_local(socket, connected, sizeof(connected) - 1, deadline);
}

static unsigned char lower_ascii(unsigned char c)
{
    return c >= 'A' && c <= 'Z' ? (unsigned char)(c + 'a' - 'A') : c;
}

static bool equal_case(const char *a, const char *b, size_t count)
{
    for (size_t i = 0; i < count; ++i) {
        if (lower_ascii((unsigned char)a[i]) != lower_ascii((unsigned char)b[i]))
            return false;
        if (!a[i])
            return true;
    }
    return true;
}

static bool token_char(unsigned char c)
{
    return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') ||
           (c >= 'a' && c <= 'z') || (c && strchr("!#$%&'*+-.^_`|~", c));
}

static bool equal_name(const char *value, const char *expected)
{
    size_t length = strlen(value);
    return length == strlen(expected) && equal_case(value, expected, length);
}

static bool decimal(const char *first, const char *end, uint64_t *number)
{
    *number = 0;
    if (first == end)
        return false;
    while (first < end) {
        unsigned int digit = (unsigned char)*first++ - (unsigned int)'0';
        if (digit > 9 || *number > (UINT64_MAX - digit) / 10)
            return false;
        *number = *number * 10 + digit;
    }
    return true;
}

static bool parse_authority(const char *start, const char *end, Destination *target,
                            bool require_port)
{
    const char *host_end, *port = NULL;
    if (start == end)
        return false;
    if (*start == '[') {
        const char *bracket = memchr(start, ']', (size_t)(end - start));
        if (!bracket)
            return false;
        host_end = bracket + 1;
        if (host_end < end) {
            if (*host_end != ':')
                return false;
            port = host_end + 1;
        }
    } else {
        host_end = memchr(start, ':', (size_t)(end - start));
        if (host_end)
            port = host_end + 1;
        else
            host_end = end;
    }
    size_t length = (size_t)(host_end - start);
    if (!length || length > MAX_HOST)
        return false;
    memcpy(target->host, start, length);
    target->host[length] = '\0';
    if (!normalize_host(target->host))
        return false;
    target->port = 80;
    if (port) {
        uint64_t number;
        if (!decimal(port, end, &number) || number == 0 || number > 65535)
            return false;
        target->port = (unsigned short)number;
    } else if (require_port) {
        return false;
    }
    return true;
}

static bool header_field(char *line, Header *header)
{
    char *colon = strchr(line, ':');
    if (!colon || colon == line)
        return false;
    for (const char *p = line; p < colon; ++p)
        if (!token_char((unsigned char)*p))
            return false;
    *colon = '\0';
    header->name = line;
    line = colon + 1;
    while (*line == ' ' || *line == '\t')
        ++line;
    header->value = line;
    char *end = line;
    while (*line) {
        unsigned char c = (unsigned char)*line++;
        if ((c < 0x20 && c != '\t') || c == 0x7f)
            return false;
        if (c != ' ' && c != '\t')
            end = line;
    }
    *end = '\0';
    return true;
}

static bool connection_header(const Header *header)
{
    return equal_name(header->name, "Connection") || equal_name(header->name, "Proxy-Connection");
}

static bool nominated(const char *value, const char *name)
{
    size_t length = strlen(name);
    while (*value) {
        while (*value == ',' || *value == ' ' || *value == '\t')
            ++value;
        const char *start = value;
        while (*value && *value != ',')
            ++value;
        const char *end = value;
        while (end > start && (end[-1] == ' ' || end[-1] == '\t'))
            --end;
        if ((size_t)(end - start) == length && equal_case(start, name, length))
            return true;
    }
    return false;
}

static bool skip_forward_header(const Header *headers, size_t count, const char *name)
{
    if (equal_name(name, "Host") || equal_name(name, "Connection") ||
        equal_case(name, "Proxy-", 6) || equal_name(name, "Keep-Alive") ||
        equal_name(name, "Upgrade") || equal_name(name, "TE"))
        return true;
    for (size_t i = 0; i < count; ++i)
        if (connection_header(&headers[i]) && nominated(headers[i].value, name))
            return true;
    return false;
}

static bool append(LocalRequest *request, const void *data, size_t size)
{
    if (size > sizeof(request->initial) - request->initial_size)
        return false;
    memcpy(request->initial + request->initial_size, data, size);
    request->initial_size += size;
    return true;
}

static bool append_string(LocalRequest *request, const char *str)
{
    return append(request, str, strlen(str));
}

static bool rewrite_headers(LocalRequest *request, const char *method, const char *path,
                            const char *authority, size_t authority_size, const char *version,
                            const Header *headers, size_t count)
{
    if (!append_string(request, method) || !append_string(request, " ") ||
        ((*path != '/') && !append_string(request, "/")) || !append_string(request, path) ||
        !append_string(request, " ") || !append_string(request, version) ||
        !append_string(request, "\r\nHost: ") || !append(request, authority, authority_size) ||
        !append_string(request, "\r\nConnection: close\r\n"))
        return false;
    for (size_t i = 0; i < count; ++i) {
        if (skip_forward_header(headers, count, headers[i].name))
            continue;
        if (!append_string(request, headers[i].name) || !append_string(request, ": ") ||
            !append_string(request, headers[i].value) || !append_string(request, "\r\n"))
            return false;
    }
    return append_string(request, "\r\n");
}

static bool parse_http(char *text, size_t header_size, LocalRequest *request)
{
    Header headers[128];
    size_t count = 0;
    bool length_seen = false, transfer_seen = false, connect;
    char *line_end = strstr(text, "\r\n");
    if (!line_end)
        return false;
    *line_end = '\0';
    char *uri = strchr(text, ' ');
    if (!uri || uri == text)
        return false;
    *uri++ = '\0';
    for (const char *p = text; *p; ++p)
        if (!token_char((unsigned char)*p))
            return false;
    char *version = strchr(uri, ' ');
    if (!version || version == uri)
        return false;
    *version++ = '\0';
    if (strcmp(version, "HTTP/1.1") && strcmp(version, "HTTP/1.0"))
        return false;
    for (const unsigned char *p = (const unsigned char *)uri; *p; ++p)
        if (*p <= 0x20 || *p >= 0x7f || *p == '#' || *p == '\\')
            return false;

    char *next = line_end + 2;
    char *end = text + header_size - 2;
    while (next < end) {
        line_end = strstr(next, "\r\n");
        if (!line_end || line_end == next || count == sizeof(headers) / sizeof(headers[0]))
            return false;
        *line_end = '\0';
        if (!header_field(next, &headers[count]))
            return false;
        const Header *field = &headers[count++];
        if (equal_name(field->name, "Content-Length")) {
            if (length_seen || !decimal(field->value, field->value + strlen(field->value),
                                        &request->body.remaining))
                return false;
            length_seen = true;
        } else if (equal_name(field->name, "Transfer-Encoding")) {
            /* 仅接受单一的 chunked 编码，以便本地转发明确正文边界。 */
            if (transfer_seen || !equal_name(field->value, "chunked") ||
                !strcmp(version, "HTTP/1.0"))
                return false;
            transfer_seen = true;
        } else if (connection_header(field) &&
                   (nominated(field->value, "Content-Length") ||
                    nominated(field->value, "Transfer-Encoding") || nominated(field->value, "Host"))) {
            return false;
        }
        next = line_end + 2;
    }

    connect = !strcmp(text, "CONNECT");
    if (connect) {
        if (transfer_seen || request->body.remaining ||
            !parse_authority(uri, uri + strlen(uri), &request->target, true))
            return false;
        request->protocol = LOCAL_HTTP_CONNECT;
        return true;
    }
    if ((length_seen && transfer_seen) || !equal_case(uri, "http://", 7))
        return false;
    const char *authority = uri + 7;
    const char *path = authority + strcspn(authority, "/?");
    if (!parse_authority(authority, path, &request->target, false))
        return false;
    request->protocol = LOCAL_HTTP_FORWARD;
    request->body.mode = transfer_seen ? BODY_CHUNKED : BODY_FIXED;
    return rewrite_headers(request, text, path, authority, (size_t)(path - authority),
                           version, headers, count);
}

static bool http_request(SOCKET socket, LocalRequest *request, ULONGLONG deadline)
{
    unsigned char bytes[MAX_HTTP_HEADER + 1];
    size_t used = 0, cursor = 0;
    while (used < MAX_HTTP_HEADER) {
        int count = read_local(socket, bytes + used, MAX_HTTP_HEADER - used, deadline);
        if (count <= 0)
            return false;
        used += (size_t)count;
        for (; cursor + 3 < used; ++cursor) {
            if (memcmp(bytes + cursor, "\r\n\r\n", 4))
                continue;
            size_t header_size = cursor + 4;
            size_t extra = used - header_size;
            /* 在末尾的 CRLF 内加上终止符，保留紧跟其后的正文。 */
            if (memchr(bytes, '\0', header_size))
                break;
            bytes[header_size - 2] = '\0';
            if (parse_http((char *)bytes, header_size, request) &&
                next_body_data(&request->body, bytes + header_size, &extra) &&
                append(request, bytes + header_size, extra))
                return true;
            http_error(socket, "400 Bad Request", deadline);
            return false;
        }
        if (cursor + 3 < used)
            break;
    }
    http_error(socket, "400 Bad Request", deadline);
    return false;
}

bool read_local_request(SOCKET socket, bool http, LocalRequest *request)
{
    ULONGLONG deadline = GetTickCount64() + HANDSHAKE_TIMEOUT;
    return http ? http_request(socket, request, deadline)
                : socks_request(socket, request, deadline);
}

static int hex_digit(unsigned char c)
{
    if (c >= '0' && c <= '9')
        return c - '0';
    c = lower_ascii(c);
    if (c >= 'a' && c <= 'f')
        return c - 'a' + 10;
    return -1;
}

static bool chunk_size(RequestBody *body)
{
    const unsigned char *cursor = (const unsigned char *)body->line;
    int digit = hex_digit(*cursor);
    if (digit < 0)
        return false;
    body->remaining = 0;
    do {
        if (body->remaining > (UINT64_MAX - (unsigned int)digit) / 16)
            return false;
        body->remaining = body->remaining * 16 + (unsigned int)digit;
        digit = hex_digit(*++cursor);
    } while (digit >= 0);
    if (*cursor && *cursor != ';')
        return false;
    while (*cursor) {
        if (*cursor < 0x20 || *cursor == 0x7f)
            return false;
        ++cursor;
    }
    body->chunk_state = body->remaining ? CHUNK_CONTENT : CHUNK_TRAILERS;
    return true;
}

static bool chunk_trailer(RequestBody *body)
{
    if (!body->line[0]) {
        body->chunk_state = CHUNK_DONE;
        return true;
    }
    Header trailer;
    return header_field(body->line, &trailer) &&
           !equal_name(trailer.name, "Content-Length") &&
           !equal_name(trailer.name, "Transfer-Encoding") &&
           !equal_name(trailer.name, "Trailer") &&
           !skip_forward_header(NULL, 0, trailer.name);
}

static bool chunk_line(RequestBody *body, unsigned char byte)
{
    if (byte == '\0' || body->line_size == sizeof(body->line))
        return false;
    if (body->chunk_state == CHUNK_TRAILERS && ++body->trailer_size > MAX_HTTP_HEADER)
        return false;
    if (body->line_size && body->line[body->line_size - 1] == '\r' && byte != '\n')
        return false;
    if (byte != '\n') {
        body->line[body->line_size++] = (char)byte;
        return true;
    }
    if (!body->line_size || body->line[body->line_size - 1] != '\r')
        return false;
    body->line[body->line_size - 1] = '\0';
    body->line_size = 0;
    return body->chunk_state == CHUNK_SIZE ? chunk_size(body) : chunk_trailer(body);
}

bool next_body_data(RequestBody *body, const unsigned char *data, size_t *size)
{
    if (body->mode == BODY_TUNNEL)
        return true;
    if (body->mode == BODY_FIXED) {
        if (*size > body->remaining)
            *size = (size_t)body->remaining;
        body->remaining -= *size;
        return true;
    }
    size_t used = 0;
    while (used < *size && body->chunk_state != CHUNK_DONE) {
        switch (body->chunk_state) {
        case CHUNK_SIZE:
        case CHUNK_TRAILERS:
            if (!chunk_line(body, data[used++]))
                return false;
            break;
        case CHUNK_CONTENT: {
            size_t count = *size - used;
            if (count > body->remaining)
                count = (size_t)body->remaining;
            used += count;
            body->remaining -= count;
            if (!body->remaining)
                body->chunk_state = CHUNK_CR;
            break;
        }
        case CHUNK_CR:
            if (data[used++] != '\r')
                return false;
            body->chunk_state = CHUNK_LF;
            break;
        case CHUNK_LF:
            if (data[used++] != '\n')
                return false;
            body->chunk_state = CHUNK_SIZE;
            break;
        case CHUNK_DONE:
            break;
        }
    }
    *size = used;
    return true;
}

bool body_finished(const RequestBody *body)
{
    return (body->mode == BODY_FIXED && !body->remaining) ||
           (body->mode == BODY_CHUNKED && body->chunk_state == CHUNK_DONE);
}
