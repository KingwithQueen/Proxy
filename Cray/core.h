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
    MAX_ALPN = 16,
    AUTH_HEX_SIZE = 56,
    HANDSHAKE_TIMEOUT = 15000,
    IDLE_TIMEOUT = 300000,
    MAX_HTTP_HEADER = 32768,
    RELAY_BUFFER_SIZE = 16384,
    MAX_WORKERS = 128
};

typedef struct {
    char server[MAX_HOST + 1];
    char sni[MAX_HOST + 1];
    unsigned short server_port, socks_port, http_port;
    char password_hash[AUTH_HEX_SIZE + 1];
    wchar_t ca_file[1024];
    char alpn_storage[MAX_ALPN][256];
    const char *alpn[MAX_ALPN + 1];
    bool verify_tls;
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

typedef struct {
    mbedtls_ssl_context context;
    SOCKET socket;
} TlsSession;

bool read_settings(Settings *settings, const wchar_t *file);
bool normalize_host(char host[MAX_HOST + 1]);

bool set_nonblocking(SOCKET socket);
int wait_socket(SOCKET socket, bool read, ULONGLONG deadline);
bool send_local(SOCKET socket, const void *data, size_t size, ULONGLONG deadline);
bool read_local_request(SOCKET socket, bool http, LocalRequest *request);
bool reply_local(SOCKET socket, LocalProtocol protocol, bool success);
bool next_body_data(RequestBody *body, const unsigned char *data, size_t *size);
bool body_finished(const RequestBody *body);

bool tls_init(TlsConfig *tls, Settings *settings);
void tls_free(TlsConfig *tls);
bool tls_connect(TlsSession *session, const TlsConfig *tls, const Settings *settings);
void tls_close(TlsSession *session);
bool send_trojan_request(TlsSession *session, const Settings *settings,
                         const Destination *target);
bool send_tls(TlsSession *session, const void *data, size_t size);
void relay(SOCKET local, TlsSession *session, RequestBody *body);

#endif
