/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "core.h"

#include <process.h>
#include <stdio.h>
#include <stdlib.h>
#include <wchar.h>

typedef struct {
    SOCKET socket;
    bool http;
} Client;

static Settings settings;
static TlsConfig tls;
static volatile LONG active_workers;

static unsigned int __stdcall serve_client(void *argument)
{
    Client client = *(Client *)argument;
    free(argument);
    LocalRequest request = { 0 };
    BOOL nodelay = TRUE;
    setsockopt(client.socket, IPPROTO_TCP, TCP_NODELAY, (const char *)&nodelay, sizeof(nodelay));
    if (!set_nonblocking(client.socket) ||
        !read_local_request(client.socket, client.http, &request))
        goto done;

    serve_upstream(client.socket, &request, &tls, &settings);
done:
    closesocket(client.socket);
    InterlockedDecrement(&active_workers);
    return 0;
}

static SOCKET start_listener(unsigned short port)
{
    SOCKET listener = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (listener == INVALID_SOCKET)
        return INVALID_SOCKET;

    BOOL exclusive = TRUE;
    struct sockaddr_in address = { 0 };
    address.sin_family = AF_INET;
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    address.sin_port = htons(port);
    if (setsockopt(listener, SOL_SOCKET, SO_EXCLUSIVEADDRUSE,
                   (const char *)&exclusive, sizeof(exclusive)) == 0 &&
        bind(listener, (const struct sockaddr *)&address, sizeof(address)) == 0 &&
        listen(listener, SOMAXCONN) == 0 && set_nonblocking(listener))
        return listener;

    int error = WSAGetLastError();
    closesocket(listener);
    WSASetLastError(error);
    return INVALID_SOCKET;
}

static bool accept_client(SOCKET listener, bool http)
{
    SOCKET socket = accept(listener, NULL, NULL);
    if (socket == INVALID_SOCKET) {
        int error = WSAGetLastError();
        return error == WSAEWOULDBLOCK || error == WSAECONNRESET;
    }
    if (InterlockedIncrement(&active_workers) <= MAX_WORKERS) {
        Client *client = malloc(sizeof(*client));
        if (client) {
            client->socket = socket;
            client->http = http;
            uintptr_t thread = _beginthreadex(NULL, 0, serve_client, client, 0, NULL);
            if (thread) {
                CloseHandle((HANDLE)thread);
                return true;
            }
            free(client);
        }
    }
    closesocket(socket);
    InterlockedDecrement(&active_workers);
    return true;
}

static bool listen_clients(SOCKET socks, SOCKET http)
{
    fd_set reads;
    FD_ZERO(&reads);
    if (socks != INVALID_SOCKET)
        FD_SET(socks, &reads);
    if (http != INVALID_SOCKET)
        FD_SET(http, &reads);
    if (select(0, &reads, NULL, NULL, NULL) == SOCKET_ERROR)
        return false;
    if (socks != INVALID_SOCKET && FD_ISSET(socks, &reads) && !accept_client(socks, false))
        return false;
    return http == INVALID_SOCKET || !FD_ISSET(http, &reads) || accept_client(http, true);
}

static void usage(FILE *out)
{
    fputs("Usage: Cray.exe -config <path or ->\n"
          "Use - to read UTF-8 JSON from standard input.\n", out);
}

int wmain(int argc, wchar_t **argv)
{
    if (argc == 2 && wcscmp(argv[1], L"--version") == 0) {
        puts("Cray 1.0.0");
        return 0;
    }
    if (argc == 2 && wcscmp(argv[1], L"--help") == 0) {
        usage(stdout);
        return 0;
    }
    if (argc != 3 || wcscmp(argv[1], L"-config") != 0) {
        usage(stderr);
        return 2;
    }

    WSADATA wsa;
    int status = WSAStartup(MAKEWORD(2, 2), &wsa);
    if (status) {
        fprintf(stderr, "Cray: Winsock could not initialize (%d)\n", status);
        return 1;
    }
    if (!read_settings(&settings, argv[2]) || (settings.use_tls && !tls_init(&tls, &settings))) {
        SecureZeroMemory(&settings, sizeof(settings));
        WSACleanup();
        return 1;
    }

    SOCKET socks = INVALID_SOCKET, http = INVALID_SOCKET;
    if (settings.socks_port) {
        socks = start_listener(settings.socks_port);
        if (socks == INVALID_SOCKET) {
            fprintf(stderr, "Cray: cannot listen on SOCKS 127.0.0.1:%u (Winsock %d)\n",
                    settings.socks_port, WSAGetLastError());
            goto cleanup;
        }
    }
    if (settings.http_port) {
        http = start_listener(settings.http_port);
        if (http == INVALID_SOCKET) {
            fprintf(stderr, "Cray: cannot listen on HTTP 127.0.0.1:%u (Winsock %d)\n",
                    settings.http_port, WSAGetLastError());
            goto cleanup;
        }
    }
    fputs("Cray: ready\n", stderr);
    fflush(stderr);
    while (listen_clients(socks, http))
        ;
    fprintf(stderr, "Cray: listener stopped (Winsock %d)\n", WSAGetLastError());

cleanup:
    if (socks != INVALID_SOCKET)
        closesocket(socks);
    if (http != INVALID_SOCKET)
        closesocket(http);
    /* 仍有线程使用共享 TLS 配置时，由进程退出统一释放资源。 */
    if (InterlockedCompareExchange(&active_workers, 0, 0) == 0) {
        if (settings.use_tls)
            tls_free(&tls);
        SecureZeroMemory(&settings, sizeof(settings));
        WSACleanup();
    }
    return 1;
}
