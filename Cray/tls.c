/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "core.h"

#include <bcrypt.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wincrypt.h>

#include "mbedtls/entropy.h"
#include "mbedtls/error.h"
#include "mbedtls/net_sockets.h"
#include "mbedtls/threading.h"
#include "psa/crypto.h"

static void tls_error(const char *action, int code)
{
    char description[192];
    mbedtls_strerror(code, description, sizeof(description));
    fprintf(stderr, "Cray: %s (%s)\n", action, description);
}

static void mutex_init(mbedtls_threading_mutex_t *mutex)
{
    InitializeSRWLock(&mutex->lock);
}

static void mutex_free(mbedtls_threading_mutex_t *mutex)
{
    (void)mutex;
}

static int mutex_lock(mbedtls_threading_mutex_t *mutex)
{
    AcquireSRWLockExclusive(&mutex->lock);
    return 0;
}

static int mutex_unlock(mbedtls_threading_mutex_t *mutex)
{
    ReleaseSRWLockExclusive(&mutex->lock);
    return 0;
}

static int random_bytes(void *ignored, unsigned char *output, size_t size)
{
    (void)ignored;
    if (size > ULONG_MAX ||
        !BCRYPT_SUCCESS(BCryptGenRandom(NULL, output, (ULONG)size,
                                      BCRYPT_USE_SYSTEM_PREFERRED_RNG)))
        return MBEDTLS_ERR_ENTROPY_SOURCE_FAILED;
    return 0;
}

static bool load_ca(TlsConfig *tls, const wchar_t *path)
{
    enum { MAX_CA_FILE = 4 * 1024 * 1024 };
    FILE *file = _wfopen(path, L"rb");
    if (!file) {
        fprintf(stderr, "Cray: could not open the configured CA file\n");
        return false;
    }
    unsigned char *data = malloc(MAX_CA_FILE + 1);
    bool success = false;
    if (data) {
        size_t size = fread(data, 1, MAX_CA_FILE, file);
        if (size && size < MAX_CA_FILE && !ferror(file)) {
            data[size] = '\0';
            int result = mbedtls_x509_crt_parse(&tls->roots, data,
                strstr((const char *)data, "-----BEGIN CERTIFICATE-----") ? size + 1 : size);
            if (result < 0)
                tls_error("cannot parse the configured CA file", result);
            else if (result > 0)
                fprintf(stderr, "Cray: invalid certificate in the configured CA file\n");
            else
                success = true;
        } else {
            fprintf(stderr, "Cray: CA file is empty, unreadable, or too large\n");
        }
        free(data);
    }
    fclose(file);
    return success;
}

bool tls_init(TlsConfig *tls, Settings *settings)
{
    mbedtls_threading_set_alt(mutex_init, mutex_free, mutex_lock, mutex_unlock);
    mbedtls_ssl_config_init(&tls->config);
    mbedtls_x509_crt_init(&tls->roots);
    if (psa_crypto_init() != PSA_SUCCESS) {
        fprintf(stderr, "Cray: cannot initialize TLS cryptography\n");
        tls_free(tls);
        return false;
    }
    int ret = mbedtls_ssl_config_defaults(&tls->config, MBEDTLS_SSL_IS_CLIENT,
        MBEDTLS_SSL_TRANSPORT_STREAM, MBEDTLS_SSL_PRESET_DEFAULT);
    if (ret) {
        tls_error("cannot configure TLS", ret);
        tls_free(tls);
        return false;
    }
    mbedtls_ssl_conf_min_tls_version(&tls->config, MBEDTLS_SSL_VERSION_TLS1_2);
    mbedtls_ssl_conf_rng(&tls->config, random_bytes, NULL);
    mbedtls_ssl_conf_session_tickets(&tls->config, MBEDTLS_SSL_SESSION_TICKETS_DISABLED);
    if (!settings->verify_tls) {
        mbedtls_ssl_conf_authmode(&tls->config, MBEDTLS_SSL_VERIFY_NONE);
        fprintf(stderr, "Cray: TLS certificate verification disabled by config\n");
    } else if (settings->ca_file[0]) {
        if (!load_ca(tls, settings->ca_file)) {
            tls_free(tls);
            return false;
        }
        mbedtls_ssl_conf_ca_chain(&tls->config, &tls->roots, NULL);
        mbedtls_ssl_conf_authmode(&tls->config, MBEDTLS_SSL_VERIFY_REQUIRED);
    } else {
        /* The Windows chain check below is mandatory before writing any proxy data. */
        mbedtls_ssl_conf_authmode(&tls->config, MBEDTLS_SSL_VERIFY_OPTIONAL);
    }
    if (settings->alpn[0]) {
        ret = mbedtls_ssl_conf_alpn_protocols(&tls->config, settings->alpn);
        if (ret) {
            tls_error("cannot configure TLS ALPN", ret);
            tls_free(tls);
            return false;
        }
    }
    return true;
}

void tls_free(TlsConfig *tls)
{
    mbedtls_ssl_config_free(&tls->config);
    mbedtls_x509_crt_free(&tls->roots);
    mbedtls_psa_crypto_free();
    mbedtls_threading_free_alt();
}

static bool verify_windows_chain(const char *hostname, const mbedtls_x509_crt *peer)
{
    bool verified = false;
    PCCERT_CONTEXT leaf = NULL;
    PCCERT_CHAIN_CONTEXT chain = NULL;
    HCERTSTORE intermediates = CertOpenStore(CERT_STORE_PROV_MEMORY, 0, 0,
                                            CERT_STORE_CREATE_NEW_FLAG, NULL);
    if (!intermediates)
        return false;

    leaf = CertCreateCertificateContext(X509_ASN_ENCODING, peer->raw.p, (DWORD)peer->raw.len);
    if (!leaf)
        goto cleanup;
    for (peer = peer->next; peer; peer = peer->next) {
        if (!CertAddEncodedCertificateToStore(intermediates, X509_ASN_ENCODING, peer->raw.p,
                    (DWORD)peer->raw.len, CERT_STORE_ADD_ALWAYS, NULL))
            goto cleanup;
    }

    char server_auth[] = szOID_PKIX_KP_SERVER_AUTH;
    LPSTR oids[] = { server_auth };
    CERT_CHAIN_PARA params = { 0 };
    params.cbSize = sizeof(params);
    params.RequestedUsage.dwType = USAGE_MATCH_TYPE_AND;
    params.RequestedUsage.Usage.cUsageIdentifier = 1;
    params.RequestedUsage.Usage.rgpszUsageIdentifier = oids;
    if (!CertGetCertificateChain(NULL, leaf, NULL, intermediates, &params,
                                 CERT_CHAIN_CACHE_ONLY_URL_RETRIEVAL, NULL, &chain))
        goto cleanup;

    wchar_t name[MAX_HOST + 1];
    SSL_EXTRA_CERT_CHAIN_POLICY_PARA ssl_policy = { 0 };
    ssl_policy.cbSize = sizeof(ssl_policy);
    ssl_policy.dwAuthType = AUTHTYPE_SERVER;
    if (hostname) {
        if (!MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, hostname, -1, name, MAX_HOST + 1))
            goto cleanup;
        ssl_policy.pwszServerName = name;
    }
    /* Numeric addresses are matched against the raw IP SAN before this check. */
    CERT_CHAIN_POLICY_PARA policy = { sizeof(policy), 0, &ssl_policy };
    CERT_CHAIN_POLICY_STATUS status = { 0 };
    status.cbSize = sizeof(status);
    if (CertVerifyCertificateChainPolicy(CERT_CHAIN_POLICY_SSL, chain, &policy, &status))
        verified = status.dwError == 0;
cleanup:
    if (!verified)
        fprintf(stderr, "Cray: Windows trust policy rejected the TLS certificate\n");
    if (chain)
        CertFreeCertificateChain(chain);
    if (leaf)
        CertFreeCertificateContext(leaf);
    CertCloseStore(intermediates, 0);
    return verified;
}

static bool ip_matches_certificate(const mbedtls_x509_crt *cert, const void *ip, size_t size)
{
    for (const mbedtls_x509_sequence *san = &cert->subject_alt_names; san; san = san->next) {
        if (san->buf.tag == (MBEDTLS_ASN1_CONTEXT_SPECIFIC | MBEDTLS_X509_SAN_IP_ADDRESS) &&
            san->buf.len == size && memcmp(san->buf.p, ip, size) == 0)
            return true;
    }
    return false;
}

static SOCKET connect_server(const Settings *settings, ULONGLONG deadline)
{
    ADDRINFOA hints = { 0 };
    ADDRINFOA *addresses = NULL;
    char port[6];
    SOCKET upstream = INVALID_SOCKET;
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_protocol = IPPROTO_TCP;
    snprintf(port, sizeof(port), "%u", settings->server_port);
    if (getaddrinfo(settings->server, port, &hints, &addresses)) {
        fprintf(stderr, "Cray: could not resolve the Trojan server\n");
        return INVALID_SOCKET;
    }
    for (ADDRINFOA *addr = addresses; addr && GetTickCount64() < deadline; addr = addr->ai_next) {
        upstream = socket(addr->ai_family, addr->ai_socktype, addr->ai_protocol);
        if (upstream == INVALID_SOCKET)
            continue;
        if (set_nonblocking(upstream)) {
            BOOL nodelay = TRUE;
            setsockopt(upstream, IPPROTO_TCP, TCP_NODELAY, (const char *)&nodelay, sizeof(nodelay));
            if (!connect(upstream, addr->ai_addr, (int)addr->ai_addrlen))
                break;
            ULONGLONG try_until = addr->ai_next ? GetTickCount64() + 4000 : deadline;
            int error = 0, size = sizeof(error);
            if (WSAGetLastError() == WSAEWOULDBLOCK &&
                wait_socket(upstream, false, try_until < deadline ? try_until : deadline) > 0 &&
                !getsockopt(upstream, SOL_SOCKET, SO_ERROR, (char *)&error, &size) && !error)
                break;
        }
        closesocket(upstream);
        upstream = INVALID_SOCKET;
    }
    freeaddrinfo(addresses);
    if (upstream == INVALID_SOCKET)
        fprintf(stderr, "Cray: could not connect to the Trojan server\n");
    return upstream;
}

static int send_bio(void *opaque, const unsigned char *buffer, size_t size)
{
    SOCKET socket = *(const SOCKET *)opaque;
    int count = send(socket, (const char *)buffer, size <= INT_MAX ? (int)size : INT_MAX, 0);
    if (count >= 0)
        return count;
    int error = WSAGetLastError();
    if (error == WSAEWOULDBLOCK)
        return MBEDTLS_ERR_SSL_WANT_WRITE;
    return error == WSAECONNRESET ? MBEDTLS_ERR_NET_CONN_RESET : MBEDTLS_ERR_NET_SEND_FAILED;
}

static int recv_bio(void *opaque, unsigned char *buffer, size_t size)
{
    SOCKET socket = *(const SOCKET *)opaque;
    int count = recv(socket, (char *)buffer, size <= INT_MAX ? (int)size : INT_MAX, 0);
    if (count >= 0)
        return count;
    int error = WSAGetLastError();
    if (error == WSAEWOULDBLOCK)
        return MBEDTLS_ERR_SSL_WANT_READ;
    return error == WSAECONNRESET ? MBEDTLS_ERR_NET_CONN_RESET : MBEDTLS_ERR_NET_RECV_FAILED;
}

static bool tls_wait(const TlsSession *session, int status, ULONGLONG deadline)
{
    return (status == MBEDTLS_ERR_SSL_WANT_READ || status == MBEDTLS_ERR_SSL_WANT_WRITE) &&
           wait_socket(session->socket, status == MBEDTLS_ERR_SSL_WANT_READ, deadline) > 0;
}

bool tls_connect(TlsSession *session, const TlsConfig *tls, const Settings *settings)
{
    ULONGLONG deadline = GetTickCount64() + HANDSHAKE_TIMEOUT;
    unsigned char ip[16];
    size_t ip_size = 0;
    mbedtls_ssl_init(&session->context);
    session->socket = connect_server(settings, deadline);
    if (session->socket == INVALID_SOCKET)
        return false;

    if (InetPtonA(AF_INET, settings->sni, ip) == 1)
        ip_size = 4;
    else if (InetPtonA(AF_INET6, settings->sni, ip) == 1)
        ip_size = 16;
    const char *hostname = ip_size ? NULL : settings->sni;
    int result = mbedtls_ssl_setup(&session->context, &tls->config);
    if (!result)
        result = mbedtls_ssl_set_hostname(&session->context, hostname);
    if (result) {
        tls_error("cannot initialize TLS session", result);
        return false;
    }
    mbedtls_ssl_set_bio(&session->context, &session->socket, send_bio, recv_bio, NULL);
    do {
        result = mbedtls_ssl_handshake(&session->context);
    } while (result && tls_wait(session, result, deadline));
    if (result) {
        if (result == MBEDTLS_ERR_SSL_WANT_READ || result == MBEDTLS_ERR_SSL_WANT_WRITE)
            fprintf(stderr, "Cray: Trojan TLS handshake timed out or disconnected\n");
        else
            tls_error("Trojan TLS handshake failed", result);
        return false;
    }
    if (!settings->verify_tls)
        return true;

    const mbedtls_x509_crt *peer = mbedtls_ssl_get_peer_cert(&session->context);
    if (!peer || (ip_size && !ip_matches_certificate(peer, ip, ip_size))) {
        fprintf(stderr, "Cray: missing TLS certificate or IP subject alternative name\n");
        return false;
    }
    uint32_t flags = mbedtls_ssl_get_verify_result(&session->context);
    if (!settings->ca_file[0])
        flags &= ~((uint32_t)MBEDTLS_X509_BADCERT_NOT_TRUSTED);
    if (flags) {
        fprintf(stderr, "Cray: TLS certificate verification failed (flags 0x%lx)\n",
                (unsigned long)flags);
        return false;
    }
    return settings->ca_file[0] || verify_windows_chain(hostname, peer);
}

void tls_close(TlsSession *session)
{
    mbedtls_ssl_free(&session->context);
    if (session->socket != INVALID_SOCKET)
        closesocket(session->socket);
}

bool send_tls(TlsSession *session, const void *data, size_t size)
{
    ULONGLONG deadline = GetTickCount64() + HANDSHAKE_TIMEOUT;
    const unsigned char *cursor = data;
    while (size && GetTickCount64() < deadline) {
        int sent = mbedtls_ssl_write(&session->context, cursor, size);
        if (sent > 0) {
            cursor += sent;
            size -= (size_t)sent;
        } else if (!tls_wait(session, sent, deadline)) {
            if (sent != MBEDTLS_ERR_SSL_WANT_READ && sent != MBEDTLS_ERR_SSL_WANT_WRITE)
                tls_error("cannot write to the Trojan server", sent);
            return false;
        }
    }
    return size == 0;
}

bool send_trojan_request(TlsSession *session, const Settings *settings, const Destination *target)
{
    unsigned char request[AUTH_HEX_SIZE + 2 + 1 + 1 + 1 + MAX_HOST + 2 + 2];
    memcpy(request, settings->password_hash, AUTH_HEX_SIZE);
    size_t length = AUTH_HEX_SIZE;
    request[length++] = '\r';
    request[length++] = '\n';
    request[length++] = 1;
    if (InetPtonA(AF_INET, target->host, request + length + 1) == 1) {
        request[length++] = 1;
        length += 4;
    } else if (InetPtonA(AF_INET6, target->host, request + length + 1) == 1) {
        request[length++] = 4;
        length += 16;
    } else {
        size_t host_length = strlen(target->host);
        request[length++] = 3;
        request[length++] = (unsigned char)host_length;
        memcpy(request + length, target->host, host_length);
        length += host_length;
    }
    request[length++] = (unsigned char)(target->port >> 8);
    request[length++] = (unsigned char)(target->port & 255);
    request[length++] = '\r';
    request[length++] = '\n';
    bool success = send_tls(session, request, length);
    SecureZeroMemory(request, sizeof(request));
    return success;
}

typedef struct {
    unsigned char data[RELAY_BUFFER_SIZE];
    size_t offset, end;
} Buffer;

typedef enum { TLS_READY, TLS_READ_READY, TLS_WRITE_READY } Readiness;

static void watch(SOCKET socket, Readiness state, fd_set *reads, fd_set *writes)
{
    if (state == TLS_READ_READY)
        FD_SET(socket, reads);
    else if (state == TLS_WRITE_READY)
        FD_SET(socket, writes);
}

static bool ready(SOCKET socket, Readiness state, fd_set *reads, fd_set *writes)
{
    return (state == TLS_READ_READY && FD_ISSET(socket, reads)) ||
           (state == TLS_WRITE_READY && FD_ISSET(socket, writes));
}

static Readiness want(int result)
{
    if (result == MBEDTLS_ERR_SSL_WANT_READ)
        return TLS_READ_READY;
    if (result == MBEDTLS_ERR_SSL_WANT_WRITE)
        return TLS_WRITE_READY;
    return TLS_READY;
}

void relay(SOCKET local, TlsSession *session, RequestBody *body)
{
    Buffer to_local = { 0 }, to_tls = { 0 };
    SOCKET upstream = session->socket;
    Readiness read_retry = TLS_READY, write_retry = TLS_READY;
    bool local_eof = false, upstream_eof = false, close_sent = false;
    ULONGLONG deadline = GetTickCount64() + IDLE_TIMEOUT;

    while (!upstream_eof || to_local.offset < to_local.end) {
        fd_set reads, writes;
        FD_ZERO(&reads);
        FD_ZERO(&writes);

        bool upload_pending = to_tls.offset < to_tls.end;
        bool send_close = local_eof && !close_sent && !upload_pending;
        if (!upstream_eof && !local_eof && !upload_pending && !body_finished(body))
            FD_SET(local, &reads);
        if (to_local.offset < to_local.end)
            FD_SET(local, &writes);

        /* Retry a blocked TLS write before calling another TLS operation. */
        bool reading = !upstream_eof && to_local.offset == to_local.end &&
                       write_retry == TLS_READY;
        bool writing = !upstream_eof && (upload_pending || send_close) &&
                       read_retry != TLS_WRITE_READY;
        Readiness read_when = read_retry == TLS_READY ? TLS_READ_READY : read_retry;
        Readiness write_when = write_retry == TLS_READY ? TLS_WRITE_READY : write_retry;
        if (reading)
            watch(upstream, read_when, &reads, &writes);
        if (writing)
            watch(upstream, write_when, &reads, &writes);
        if (!reads.fd_count && !writes.fd_count)
            return;

        ULONGLONG now = GetTickCount64();
        if (now >= deadline)
            return;
        bool decrypted_pending = reading && read_when == TLS_READ_READY &&
                                 mbedtls_ssl_check_pending(&session->context);
        ULONGLONG remaining = decrypted_pending ? 0 : deadline - now;
        struct timeval wait = { (long)(remaining / 1000), (long)((remaining % 1000) * 1000) };
        int ready_count = select(0, &reads, &writes, NULL, &wait);
        if (ready_count < 0 || (!ready_count && !decrypted_pending))
            return;
        bool progress = false;

        if (FD_ISSET(local, &writes)) {
            int sent = send(local, (const char *)to_local.data + to_local.offset,
                            (int)(to_local.end - to_local.offset), 0);
            if (sent > 0) {
                to_local.offset += (size_t)sent;
                progress = true;
            } else if (!sent || WSAGetLastError() != WSAEWOULDBLOCK) {
                return;
            }
        }
        if (FD_ISSET(local, &reads)) {
            int received = recv(local, (char *)to_tls.data, sizeof(to_tls.data), 0);
            if (received > 0) {
                to_tls.end = (size_t)received;
                to_tls.offset = 0;
                if (!next_body_data(body, to_tls.data, &to_tls.end))
                    return;
                progress = true;
            } else if (!received) {
                if (body->mode != BODY_TUNNEL)
                    return;
                local_eof = true;
            } else if (WSAGetLastError() != WSAEWOULDBLOCK) {
                return;
            }
        }

        if (writing && ready(upstream, write_when, &reads, &writes)) {
            int sent = send_close ? mbedtls_ssl_close_notify(&session->context) :
                mbedtls_ssl_write(&session->context, to_tls.data + to_tls.offset,
                                  to_tls.end - to_tls.offset);
            write_retry = want(sent);
            if (send_close && sent == 0) {
                close_sent = true;
                progress = true;
            } else if (sent > 0) {
                to_tls.offset += (size_t)sent;
                progress = true;
            } else if (write_retry == TLS_READY) {
                return;
            }
        }
        if (reading && write_retry == TLS_READY &&
            (decrypted_pending || ready(upstream, read_when, &reads, &writes))) {
            int received = mbedtls_ssl_read(&session->context, to_local.data, sizeof(to_local.data));
            read_retry = want(received);
            if (received > 0) {
                to_local.offset = 0;
                to_local.end = (size_t)received;
                progress = true;
            } else if (read_retry == TLS_READY) {
                upstream_eof = true;
            }
        }
        if (progress)
            deadline = GetTickCount64() + IDLE_TIMEOUT;
    }
}
