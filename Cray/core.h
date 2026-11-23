/* SPDX-License-Identifier: GPL-3.0-or-later */
#ifndef CRAY_CORE_H
#define CRAY_CORE_H

#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <stdbool.h>
#include <stdint.h>

#include "mbedtls/ssl.h"

enum {
    MAX_HOST = 255,
    V2RAY_ADDRESS_SIZE = 4 + MAX_HOST,
    SOCKS_ADDRESS_SIZE = 4 + MAX_HOST,
    MAX_ALPN = 16,
    AUTH_HEX_SIZE = 56,
    HANDSHAKE_TIMEOUT = 15000,
    IDLE_TIMEOUT = 300000,
    MAX_HTTP_HEADER = 32768,
    RELAY_BUFFER_SIZE = 16384,
    MAX_WORKERS = 128
};

typedef enum {
    OUTBOUND_TROJAN,
    OUTBOUND_VMESS,
    OUTBOUND_VLESS,
    OUTBOUND_ANYTLS
} OutboundProtocol;

typedef enum {
    VMESS_SECURITY_AES_128_GCM = 3,
    VMESS_SECURITY_CHACHA20_POLY1305 = 4,
    VMESS_SECURITY_NONE = 5,
    VMESS_SECURITY_ZERO = 6
} VmessSecurity;

typedef struct {
    char server[MAX_HOST + 1];
    char sni[MAX_HOST + 1];
    unsigned short server_port, socks_port, http_port;
    char password_hash[AUTH_HEX_SIZE + 1];
    unsigned char anytls_password_hash[32];
    unsigned char uuid[16];
    OutboundProtocol protocol;
    VmessSecurity vmess_security;
    wchar_t ca_file[1024];
    char alpn_storage[MAX_ALPN][256];
    const char *alpn[MAX_ALPN + 1];
    bool use_tls, verify_tls;
} Settings;

typedef struct {
    char host[MAX_HOST + 1];
    unsigned short port;
} Destination;

typedef enum {
    BODY_TUNNEL,
    BODY_FIXED,
    BODY_CHUNKED
} BodyMode;

typedef enum {
    CHUNK_SIZE,
    CHUNK_CONTENT,
    CHUNK_CR,
    CHUNK_LF,
    CHUNK_TRAILERS,
    CHUNK_DONE
} ChunkState;

typedef struct {
    uint64_t remaining;
    BodyMode mode;
    ChunkState chunk_state;
    char line[4096];
    size_t line_size;
    size_t trailer_size;
} RequestBody;

typedef enum {
    LOCAL_INVALID,
    LOCAL_SOCKS,
    LOCAL_HTTP_CONNECT,
    LOCAL_HTTP_FORWARD
} LocalProtocol;

typedef struct {
    Destination target;
    LocalProtocol protocol;
    RequestBody body;
    unsigned char initial[MAX_HTTP_HEADER + 256];
    size_t initial_size;
} LocalRequest;

typedef struct {
    mbedtls_ssl_config config;
    mbedtls_x509_crt roots;
} TlsConfig;

bool read_settings(Settings *settings, const wchar_t *file);
bool normalize_host(char host[MAX_HOST + 1]);
size_t write_v2ray_address(unsigned char output[V2RAY_ADDRESS_SIZE], const Destination *target);
size_t write_socks_address(unsigned char output[SOCKS_ADDRESS_SIZE], const Destination *target);

bool set_nonblocking(SOCKET socket);
int wait_socket(SOCKET socket, bool read, ULONGLONG deadline);
bool send_local(SOCKET socket, const void *data, size_t size, ULONGLONG deadline);
bool read_local_request(SOCKET socket, bool http, LocalRequest *request);
bool reply_local(SOCKET socket, LocalProtocol protocol, bool success);
bool next_body_data(RequestBody *body, const unsigned char *data, size_t *size);
bool body_finished(const RequestBody *body);

bool tls_init(TlsConfig *tls, Settings *settings);
void tls_free(TlsConfig *tls);
void serve_upstream(SOCKET local, LocalRequest *request, const TlsConfig *tls,
                    const Settings *settings);

#endif
