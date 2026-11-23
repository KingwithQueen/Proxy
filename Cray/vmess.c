/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "vmess.h"

#include <bcrypt.h>
#include <stdlib.h>
#include <string.h>

#include "mbedtls/aes.h"
#include "mbedtls/chachapoly.h"
#include "mbedtls/gcm.h"
#include "mbedtls/md5.h"
#include "mbedtls/platform_util.h"
#include "mbedtls/sha256.h"

enum {
    KEY_SIZE = 16,
    NONCE_SIZE = 12,
    TAG_SIZE = 16,
    SHA256_SIZE = 32,
    SHA256_BLOCK_SIZE = 64,
    SHAKE128_RATE = 168,
    AUTH_ID_SIZE = 16,
    CONNECTION_NONCE_SIZE = 8,
    LENGTH_SIZE = 2,
    ENCRYPTED_LENGTH_SIZE = LENGTH_SIZE + TAG_SIZE,
    MAX_HEADER_PADDING = 15,
    MAX_CHUNK_PADDING = 63,
    MAX_REQUEST_HEADER = 38 + V2RAY_ADDRESS_SIZE + MAX_HEADER_PADDING + 4,
    MAX_RESPONSE_HEADER = 4 + 255,
    MAX_CHUNK = 16384,
    MAX_CHUNK_PAYLOAD = MAX_CHUNK - LENGTH_SIZE - TAG_SIZE - MAX_CHUNK_PADDING,
    KDF_MAX_DEPTH = 4,
    KDF_MAX_INPUT = SHA256_SIZE + SHA256_BLOCK_SIZE * KDF_MAX_DEPTH,
    OPTION_CHUNK_STREAM = 1,
    OPTION_CHUNK_MASKING = 4,
    OPTION_GLOBAL_PADDING = 8
};

_Static_assert(VMESS_WRITE_OVERHEAD >=
    ((RELAY_BUFFER_SIZE + MAX_CHUNK_PAYLOAD - 1) / MAX_CHUNK_PAYLOAD) *
    (LENGTH_SIZE + TAG_SIZE + MAX_CHUNK_PADDING), "VMess relay buffer is too small");

typedef struct {
    uint64_t lanes[25];
    size_t offset;
} Shake128;

typedef struct {
    Shake128 mask;
    union {
        mbedtls_gcm_context gcm;
        mbedtls_chachapoly_context chacha;
    } context;
    unsigned char nonce[NONCE_SIZE];
    uint32_t counter;
} BodyCipher;

typedef enum {
    READ_HEADER_LENGTH,
    READ_HEADER,
    READ_CHUNK_LENGTH,
    READ_CHUNK,
    READ_RAW,
    READ_EOF,
    READ_ERROR
} ReadStage;

struct VmessSession {
    void *stream;
    VmessSend send;
    VmessSecurity security;
    bool send_finished, send_failed;
    unsigned char response_verifier;
    unsigned char response_key[KEY_SIZE], response_iv[KEY_SIZE];
    BodyCipher request, response;
    ReadStage read_stage;
    size_t wire_used, wire_needed, padding;
    size_t plain_offset, plain_size;
    unsigned char input[MAX_CHUNK];
};

typedef struct {
    const unsigned char *data;
    size_t size;
} KdfPart;

static uint16_t read_uint16(const unsigned char *data)
{
    return (uint16_t)((uint16_t)data[0] << 8 | data[1]);
}

static void write_uint16(unsigned char *data, uint16_t value)
{
    data[0] = (unsigned char)(value >> 8);
    data[1] = (unsigned char)value;
}

static void write_uint32(unsigned char *data, uint32_t value)
{
    write_uint16(data, (uint16_t)(value >> 16));
    write_uint16(data + 2, (uint16_t)value);
}

static bool random_fill(unsigned char *output, size_t size)
{
    return BCRYPT_SUCCESS(BCryptGenRandom(NULL, output, (ULONG)size,
                                           BCRYPT_USE_SYSTEM_PREFERRED_RNG));
}

static uint64_t rotate_left(uint64_t value, unsigned int shift)
{
    return (value << shift) | (value >> (64 - shift));
}

/* SHAKE128 使用 FIPS 202 的 Keccak-f[1600] 置换。 */
static void keccak_permute(uint64_t lanes[25])
{
    static const uint64_t round_constants[24] = {
        0x0000000000000001ULL, 0x0000000000008082ULL, 0x800000000000808aULL,
        0x8000000080008000ULL, 0x000000000000808bULL, 0x0000000080000001ULL,
        0x8000000080008081ULL, 0x8000000000008009ULL, 0x000000000000008aULL,
        0x0000000000000088ULL, 0x0000000080008009ULL, 0x000000008000000aULL,
        0x000000008000808bULL, 0x800000000000008bULL, 0x8000000000008089ULL,
        0x8000000000008003ULL, 0x8000000000008002ULL, 0x8000000000000080ULL,
        0x000000000000800aULL, 0x800000008000000aULL, 0x8000000080008081ULL,
        0x8000000000008080ULL, 0x0000000080000001ULL, 0x8000000080008008ULL
    };
    static const unsigned char rotations[24] = {
        1, 3, 6, 10, 15, 21, 28, 36, 45, 55, 2, 14, 27, 41, 56, 8, 25, 43, 62, 18, 39, 61, 20, 44
    };
    static const unsigned char positions[24] = {
        10, 7, 11, 17, 18, 3, 5, 16, 8, 21, 24, 4, 15, 23, 19, 13, 12, 2, 20, 14, 22, 9, 6, 1
    };

    for (size_t round = 0; round < 24; round++) {
        uint64_t line[5];
        for (size_t x = 0; x < 5; x++)
            line[x] = lanes[x] ^ lanes[x + 5] ^ lanes[x + 10] ^ lanes[x + 15] ^ lanes[x + 20];
        for (size_t x = 0; x < 5; x++) {
            uint64_t delta = line[(x + 4) % 5] ^ rotate_left(line[(x + 1) % 5], 1);
            for (size_t y = 0; y < 25; y += 5)
                lanes[y + x] ^= delta;
        }

        uint64_t lane = lanes[1];
        for (size_t i = 0; i < 24; i++) {
            size_t position = positions[i];
            uint64_t saved = lanes[position];
            lanes[position] = rotate_left(lane, rotations[i]);
            lane = saved;
        }

        for (size_t y = 0; y < 25; y += 5) {
            memcpy(line, lanes + y, sizeof(line));
            for (size_t x = 0; x < 5; x++)
                lanes[y + x] = line[x] ^ (~line[(x + 1) % 5] & line[(x + 2) % 5]);
        }
        lanes[0] ^= round_constants[round];
    }
}

static void shake_init(Shake128 *shake, const unsigned char iv[KEY_SIZE])
{
    memset(shake, 0, sizeof(*shake));
    for (size_t i = 0; i < KEY_SIZE; i++)
        shake->lanes[i / 8] |= (uint64_t)iv[i] << (8 * (i % 8));
    shake->lanes[KEY_SIZE / 8] = 0x1f;
    shake->lanes[SHAKE128_RATE / 8 - 1] = UINT64_C(1) << 63;
    keccak_permute(shake->lanes);
}

static uint16_t shake_next(Shake128 *shake)
{
    if (shake->offset == SHAKE128_RATE) {
        keccak_permute(shake->lanes);
        shake->offset = 0;
    }
    size_t offset = shake->offset;
    shake->offset += 2;
    uint16_t bytes = (uint16_t)(shake->lanes[offset / 8] >> (8 * (offset % 8)));
    return (uint16_t)((bytes << 8) | (bytes >> 8));
}

/* 每一层都用内层 HMAC 作为哈希函数，最内层才直接使用 SHA-256。 */
static bool kdf_hmac(const KdfPart *path, size_t depth, const unsigned char *data,
                     size_t size, unsigned char output[SHA256_SIZE])
{
    if (!depth)
        return mbedtls_sha256(data, size, output, 0) == 0;

    const KdfPart *salt = &path[depth - 1];
    unsigned char input[KDF_MAX_INPUT];
    unsigned char digest[SHA256_SIZE];
    for (size_t i = 0; i < SHA256_BLOCK_SIZE; i++) {
        unsigned char byte = i < salt->size ? salt->data[i] : 0;
        input[i] = (unsigned char)(byte ^ 0x36);
    }
    memcpy(input + SHA256_BLOCK_SIZE, data, size);
    bool success = kdf_hmac(path, depth - 1, input, SHA256_BLOCK_SIZE + size, digest);
    if (success) {
        for (size_t i = 0; i < SHA256_BLOCK_SIZE; i++)
            input[i] ^= 0x36 ^ 0x5c;
        memcpy(input + SHA256_BLOCK_SIZE, digest, sizeof(digest));
        success = kdf_hmac(path, depth - 1, input, SHA256_BLOCK_SIZE + sizeof(digest), output);
    }
    mbedtls_platform_zeroize(input, sizeof(input));
    mbedtls_platform_zeroize(digest, sizeof(digest));
    return success;
}

static bool derive_key(const unsigned char key[KEY_SIZE], const char *label,
                       const unsigned char *auth_id, const unsigned char *connection_nonce,
                       unsigned char output[SHA256_SIZE])
{
    static const unsigned char root[] = "VMess AEAD KDF";
    KdfPart path[] = {
        { root, sizeof(root) - 1 },
        { (const unsigned char *)label, strlen(label) },
        { auth_id, AUTH_ID_SIZE },
        { connection_nonce, CONNECTION_NONCE_SIZE }
    };
    return kdf_hmac(path, auth_id ? KDF_MAX_DEPTH : 2, key, KEY_SIZE, output);
}

static uint32_t crc32_ieee(const unsigned char *data, size_t size)
{
    uint32_t crc = UINT32_MAX;
    for (size_t i = 0; i < size; i++) {
        crc ^= data[i];
        for (size_t bit = 0; bit < 8; bit++)
            crc = (crc >> 1) ^ (UINT32_C(0xedb88320) & (0u - (crc & 1u)));
    }
    return ~crc;
}

static uint32_t fnv1a(const unsigned char *data, size_t size)
{
    uint32_t hash = UINT32_C(2166136261);
    for (size_t i = 0; i < size; i++)
        hash = (hash ^ data[i]) * UINT32_C(16777619);
    return hash;
}

static bool create_auth_id(const unsigned char command_key[KEY_SIZE], unsigned char output[AUTH_ID_SIZE])
{
    FILETIME now;
    GetSystemTimeAsFileTime(&now);
    uint64_t ticks = ((uint64_t)now.dwHighDateTime << 32) | now.dwLowDateTime;
    uint64_t seconds = ticks / UINT64_C(10000000) - UINT64_C(11644473600);

    unsigned char plaintext[AUTH_ID_SIZE];
    write_uint32(plaintext, (uint32_t)(seconds >> 32));
    write_uint32(plaintext + 4, (uint32_t)seconds);
    if (!random_fill(plaintext + 8, 4))
        return false;
    write_uint32(plaintext + 12, crc32_ieee(plaintext, 12));

    mbedtls_aes_context aes;
    mbedtls_aes_init(&aes);
    unsigned char key[SHA256_SIZE];
    bool success = derive_key(command_key, "AES Auth ID Encryption", NULL, NULL, key) &&
        mbedtls_aes_setkey_enc(&aes, key, 128) == 0 &&
        mbedtls_aes_crypt_ecb(&aes, MBEDTLS_AES_ENCRYPT, plaintext, output) == 0;
    mbedtls_aes_free(&aes);
    mbedtls_platform_zeroize(key, sizeof(key));
    return success;
}

static bool encrypt_header_part(const unsigned char command_key[KEY_SIZE], bool length_part,
                                const unsigned char *auth_id, const unsigned char *connection_nonce,
                                const unsigned char *plaintext, size_t size, unsigned char *wire)
{
    unsigned char key[SHA256_SIZE], nonce[SHA256_SIZE];
    const char *key_label = length_part ? "VMess Header AEAD Key_Length" : "VMess Header AEAD Key";
    const char *iv_label = length_part ? "VMess Header AEAD Nonce_Length" : "VMess Header AEAD Nonce";
    mbedtls_gcm_context gcm;
    mbedtls_gcm_init(&gcm);
    bool success = derive_key(command_key, key_label, auth_id, connection_nonce, key) &&
        derive_key(command_key, iv_label, auth_id, connection_nonce, nonce) &&
        mbedtls_gcm_setkey(&gcm, MBEDTLS_CIPHER_ID_AES, key, 128) == 0 &&
        mbedtls_gcm_crypt_and_tag(&gcm, MBEDTLS_GCM_ENCRYPT, size, nonce, NONCE_SIZE,
                                 auth_id, AUTH_ID_SIZE, plaintext, wire, TAG_SIZE, wire + size) == 0;
    mbedtls_gcm_free(&gcm);
    mbedtls_platform_zeroize(key, sizeof(key));
    mbedtls_platform_zeroize(nonce, sizeof(nonce));
    return success;
}

static bool decrypt_header_part(VmessSession *session, bool length_part, size_t size)
{
    unsigned char key[SHA256_SIZE], nonce[SHA256_SIZE];
    const char *key_label = length_part ? "AEAD Resp Header Len Key" : "AEAD Resp Header Key";
    const char *iv_label = length_part ? "AEAD Resp Header Len IV" : "AEAD Resp Header IV";
    mbedtls_gcm_context gcm;
    mbedtls_gcm_init(&gcm);
    bool success = derive_key(session->response_key, key_label, NULL, NULL, key) &&
        derive_key(session->response_iv, iv_label, NULL, NULL, nonce) &&
        mbedtls_gcm_setkey(&gcm, MBEDTLS_CIPHER_ID_AES, key, 128) == 0 &&
        mbedtls_gcm_auth_decrypt(&gcm, size, nonce, NONCE_SIZE, NULL, 0,
                                 session->input + size, TAG_SIZE, session->input, session->input) == 0;
    mbedtls_gcm_free(&gcm);
    mbedtls_platform_zeroize(key, sizeof(key));
    mbedtls_platform_zeroize(nonce, sizeof(nonce));
    return success;
}

static size_t body_tag_size(VmessSecurity security)
{
    return security == VMESS_SECURITY_AES_128_GCM || security == VMESS_SECURITY_CHACHA20_POLY1305 ? TAG_SIZE : 0;
}

static bool send_request_header(VmessSession *session, const Settings *settings,
                                const Destination *target, const unsigned char *random)
{
    static const unsigned char command_salt[] = "c48619fe-8f02-49e0-b9e9-edf763e17e21";
    unsigned char command_input[KEY_SIZE + sizeof(command_salt) - 1];
    unsigned char command_key[KEY_SIZE];
    unsigned char plaintext[MAX_REQUEST_HEADER];
    unsigned char wire[AUTH_ID_SIZE + ENCRYPTED_LENGTH_SIZE + CONNECTION_NONCE_SIZE + MAX_REQUEST_HEADER + TAG_SIZE];
    unsigned char *length_wire = wire + AUTH_ID_SIZE;
    unsigned char *connection_nonce = length_wire + ENCRYPTED_LENGTH_SIZE;
    unsigned char *header_wire = connection_nonce + CONNECTION_NONCE_SIZE;
    size_t address_size = write_v2ray_address(plaintext + 38, target);
    if (!address_size)
        return false;

    /* 请求头依次由认证 ID、加密长度、连接随机数及加密指令组成。 */
    plaintext[0] = 1;
    memcpy(plaintext + 1, random, 2 * KEY_SIZE + 1);
    plaintext[34] = 0;
    if (session->security != VMESS_SECURITY_ZERO)
        plaintext[34] = OPTION_CHUNK_STREAM | OPTION_CHUNK_MASKING;
    if (body_tag_size(session->security))
        plaintext[34] |= OPTION_GLOBAL_PADDING;
    size_t padding = random[2 * KEY_SIZE + 1] & MAX_HEADER_PADDING;
    VmessSecurity wire_security = session->security == VMESS_SECURITY_ZERO ? VMESS_SECURITY_NONE : session->security;
    plaintext[35] = (unsigned char)((padding << 4) | wire_security);
    plaintext[36] = 0;
    plaintext[37] = 1;
    size_t size = 38 + address_size;
    memcpy(plaintext + size, random + 2 * KEY_SIZE + 2, padding);
    size += padding;
    write_uint32(plaintext + size, fnv1a(plaintext, size));
    size += 4;

    unsigned char length[LENGTH_SIZE];
    write_uint16(length, (uint16_t)size);
    memcpy(command_input, settings->uuid, KEY_SIZE);
    memcpy(command_input + KEY_SIZE, command_salt, sizeof(command_salt) - 1);
    bool success = mbedtls_md5(command_input, sizeof(command_input), command_key) == 0 &&
        create_auth_id(command_key, wire) && random_fill(connection_nonce, CONNECTION_NONCE_SIZE) &&
        encrypt_header_part(command_key, true, wire, connection_nonce, length, sizeof(length), length_wire) &&
        encrypt_header_part(command_key, false, wire, connection_nonce, plaintext, size, header_wire) &&
        session->send(session->stream, wire, (size_t)(header_wire - wire) + size + TAG_SIZE);
    mbedtls_platform_zeroize(command_input, sizeof(command_input));
    mbedtls_platform_zeroize(command_key, sizeof(command_key));
    mbedtls_platform_zeroize(plaintext, sizeof(plaintext));
    return success;
}

static bool set_body_key(BodyCipher *cipher, VmessSecurity security,
                         const unsigned char key[KEY_SIZE], const unsigned char iv[KEY_SIZE])
{
    memcpy(cipher->nonce, iv, sizeof(cipher->nonce));
    if (security != VMESS_SECURITY_ZERO)
        shake_init(&cipher->mask, iv);
    if (security == VMESS_SECURITY_AES_128_GCM)
        return mbedtls_gcm_setkey(&cipher->context.gcm, MBEDTLS_CIPHER_ID_AES, key, 128) == 0;
    if (security == VMESS_SECURITY_CHACHA20_POLY1305) {
        unsigned char expanded[2 * KEY_SIZE];
        bool success = mbedtls_md5(key, KEY_SIZE, expanded) == 0 &&
            mbedtls_md5(expanded, KEY_SIZE, expanded + KEY_SIZE) == 0 &&
            mbedtls_chachapoly_setkey(&cipher->context.chacha, expanded) == 0;
        mbedtls_platform_zeroize(expanded, sizeof(expanded));
        return success;
    }
    return true;
}

VmessSession *vmess_open(const Settings *settings, const Destination *target, void *stream, VmessSend send)
{
    VmessSession *session = calloc(1, sizeof(*session));
    if (!session)
        return NULL;
    session->stream = stream;
    session->send = send;
    session->security = settings->vmess_security;
    session->read_stage = READ_HEADER_LENGTH;
    session->wire_needed = ENCRYPTED_LENGTH_SIZE;
    switch (session->security) {
    case VMESS_SECURITY_AES_128_GCM:
        mbedtls_gcm_init(&session->request.context.gcm);
        mbedtls_gcm_init(&session->response.context.gcm);
        break;
    case VMESS_SECURITY_CHACHA20_POLY1305:
        mbedtls_chachapoly_init(&session->request.context.chacha);
        mbedtls_chachapoly_init(&session->response.context.chacha);
        break;
    case VMESS_SECURITY_NONE:
    case VMESS_SECURITY_ZERO:
        break;
    default:
        free(session);
        return NULL;
    }

    unsigned char random[2 * KEY_SIZE + 2 + MAX_HEADER_PADDING];
    unsigned char response_key[SHA256_SIZE], response_iv[SHA256_SIZE];
    bool success = random_fill(random, sizeof(random)) &&
        mbedtls_sha256(random, KEY_SIZE, response_iv, 0) == 0 &&
        mbedtls_sha256(random + KEY_SIZE, KEY_SIZE, response_key, 0) == 0;
    if (success) {
        memcpy(session->response_key, response_key, KEY_SIZE);
        memcpy(session->response_iv, response_iv, KEY_SIZE);
        session->response_verifier = random[2 * KEY_SIZE];
        success = set_body_key(&session->request, session->security, random + KEY_SIZE, random) &&
            set_body_key(&session->response, session->security, response_key, response_iv) &&
            send_request_header(session, settings, target, random);
    }
    mbedtls_platform_zeroize(random, sizeof(random));
    mbedtls_platform_zeroize(response_key, sizeof(response_key));
    mbedtls_platform_zeroize(response_iv, sizeof(response_iv));
    if (success)
        return session;
    vmess_close(session);
    return NULL;
}

void vmess_close(VmessSession *session)
{
    if (!session)
        return;
    if (session->security == VMESS_SECURITY_AES_128_GCM) {
        mbedtls_gcm_free(&session->request.context.gcm);
        mbedtls_gcm_free(&session->response.context.gcm);
    } else if (session->security == VMESS_SECURITY_CHACHA20_POLY1305) {
        mbedtls_chachapoly_free(&session->request.context.chacha);
        mbedtls_chachapoly_free(&session->response.context.chacha);
    }
    mbedtls_platform_zeroize(session, sizeof(*session));
    free(session);
}

static bool next_chunk_nonce(BodyCipher *cipher)
{
    /* 分块序号只占两字节，溢出后终止会话以免重复使用 nonce。 */
    if (cipher->counter > UINT16_MAX)
        return false;
    write_uint16(cipher->nonce, (uint16_t)cipher->counter++);
    return true;
}

static bool encrypt_chunk(VmessSession *session, const unsigned char *plain, size_t size,
                          unsigned char *wire)
{
    BodyCipher *cipher = &session->request;
    if (session->security == VMESS_SECURITY_NONE) {
        if (size)
            memcpy(wire, plain, size);
        return true;
    }
    if (!next_chunk_nonce(cipher))
        return false;
    if (session->security == VMESS_SECURITY_AES_128_GCM)
        return mbedtls_gcm_crypt_and_tag(&cipher->context.gcm, MBEDTLS_GCM_ENCRYPT,
                                        size, cipher->nonce, NONCE_SIZE, NULL, 0,
                                        plain, wire, TAG_SIZE, wire + size) == 0;
    return mbedtls_chachapoly_encrypt_and_tag(&cipher->context.chacha, size, cipher->nonce,
                                             NULL, 0, plain, wire, wire + size) == 0;
}

static bool decrypt_chunk(VmessSession *session, size_t size)
{
    BodyCipher *cipher = &session->response;
    unsigned char *wire = session->input;
    if (session->security == VMESS_SECURITY_NONE)
        return true;
    if (!next_chunk_nonce(cipher))
        return false;
    if (session->security == VMESS_SECURITY_AES_128_GCM)
        return mbedtls_gcm_auth_decrypt(&cipher->context.gcm, size, cipher->nonce, NONCE_SIZE,
                                        NULL, 0, wire + size, TAG_SIZE, wire, wire) == 0;
    return mbedtls_chachapoly_auth_decrypt(&cipher->context.chacha, size, cipher->nonce,
                                           NULL, 0, wire + size, wire, wire) == 0;
}

static size_t next_chunk_padding(BodyCipher *cipher, size_t tag_size)
{
    return tag_size ? shake_next(&cipher->mask) % (MAX_CHUNK_PADDING + 1) : 0;
}

static bool send_chunk(VmessSession *session, const unsigned char *data, size_t size)
{
    unsigned char wire[MAX_CHUNK];
    size_t tag_size = body_tag_size(session->security);
    size_t padding = next_chunk_padding(&session->request, tag_size);
    uint16_t mask = shake_next(&session->request.mask);
    size_t payload_size = size + tag_size + padding;
    write_uint16(wire, (uint16_t)payload_size ^ mask);
    return encrypt_chunk(session, data, size, wire + LENGTH_SIZE) &&
        (!padding || random_fill(wire + LENGTH_SIZE + size + tag_size, padding)) &&
        session->send(session->stream, wire, LENGTH_SIZE + payload_size);
}

bool vmess_send(VmessSession *session, const unsigned char *data, size_t size)
{
    if (session->send_finished || session->send_failed)
        return false;
    if (session->security == VMESS_SECURITY_ZERO) {
        session->send_failed = size && !session->send(session->stream, data, size);
        return !session->send_failed;
    }
    while (size) {
        size_t chunk_size = size < MAX_CHUNK_PAYLOAD ? size : MAX_CHUNK_PAYLOAD;
        if (!send_chunk(session, data, chunk_size)) {
            session->send_failed = true;
            return false;
        }
        data += chunk_size;
        size -= chunk_size;
    }
    return true;
}

bool vmess_finish(VmessSession *session)
{
    if (!session->send_finished && !session->send_failed) {
        const unsigned char empty[] = {0};
        session->send_finished = session->security == VMESS_SECURITY_ZERO || send_chunk(session, empty, 0);
        session->send_failed = !session->send_finished;
    }
    return session->send_finished;
}

static int read_wire(VmessSession *session, VmessRecv recv)
{
    while (session->wire_used < session->wire_needed) {
        size_t remaining = session->wire_needed - session->wire_used;
        int length = recv(session->stream, session->input + session->wire_used, remaining);
        if (length == 0)
            return session->read_stage == READ_CHUNK_LENGTH && !session->wire_used ? 0 : -1;
        if (length < 0)
            return length == -2 ? -2 : -1;
        if ((size_t)length > remaining)
            return -1;
        session->wire_used += (size_t)length;
    }
    session->wire_used = 0;
    return 1;
}

static bool decode_wire(VmessSession *session)
{
    size_t size;
    size_t tag_size = body_tag_size(session->security);
    switch (session->read_stage) {
    case READ_HEADER_LENGTH:
        if (!decrypt_header_part(session, true, LENGTH_SIZE))
            return false;
        size = read_uint16(session->input);
        if (size < 4 || size > MAX_RESPONSE_HEADER)
            return false;
        session->read_stage = READ_HEADER;
        session->wire_needed = size + TAG_SIZE;
        return true;
    case READ_HEADER:
        size = session->wire_needed - TAG_SIZE;
        if (!decrypt_header_part(session, false, size) ||
            session->input[0] != session->response_verifier ||
            size != (size_t)4 + session->input[3])
            return false;
        session->read_stage = session->security == VMESS_SECURITY_ZERO ? READ_RAW : READ_CHUNK_LENGTH;
        session->wire_needed = LENGTH_SIZE;
        return true;
    case READ_CHUNK_LENGTH:
        /* 填充长度比掩码先从同一 SHAKE128 字节流中取出。 */
        session->padding = next_chunk_padding(&session->response, tag_size);
        size = read_uint16(session->input) ^ shake_next(&session->response.mask);
        if (size < tag_size + session->padding || size > MAX_CHUNK)
            return false;
        session->read_stage = READ_CHUNK;
        session->wire_needed = size;
        return true;
    case READ_CHUNK:
        size = session->wire_needed - session->padding - tag_size;
        if (!decrypt_chunk(session, size))
            return false;
        session->plain_offset = 0;
        session->plain_size = size;
        session->read_stage = size ? READ_CHUNK_LENGTH : READ_EOF;
        session->wire_needed = LENGTH_SIZE;
        return true;
    default:
        return false;
    }
}

int vmess_read(VmessSession *session, unsigned char *output, size_t capacity, VmessRecv recv)
{
    if (!capacity)
        return -2;

    while (session->plain_offset == session->plain_size) {
        if (session->read_stage == READ_EOF)
            return 0;
        if (session->read_stage == READ_ERROR)
            return -1;
        if (session->read_stage == READ_RAW) {
            size_t limit = capacity < MAX_CHUNK ? capacity : MAX_CHUNK;
            int length = recv(session->stream, output, limit);
            if (length == -2)
                return -2;
            if (length < 0 || (size_t)length > limit) {
                session->read_stage = READ_ERROR;
                return -1;
            }
            if (length == 0)
                session->read_stage = READ_EOF;
            return length;
        }

        int result = read_wire(session, recv);
        if (result == -2)
            return -2;
        if (result == 1 && decode_wire(session))
            continue;
        session->read_stage = result == 0 ? READ_EOF : READ_ERROR;
        return result == 0 ? 0 : -1;
    }

    size_t size = session->plain_size - session->plain_offset;
    if (size > capacity)
        size = capacity;
    memcpy(output, session->input + session->plain_offset, size);
    session->plain_offset += size;
    return (int)size;
}
