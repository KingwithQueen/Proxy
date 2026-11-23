/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "vless.h"

#include <limits.h>
#include <string.h>

enum {
    VLESS_VERSION = 0,
    VLESS_TCP_CONNECT = 1
};

bool vless_open(VlessSession *session, const Settings *settings, const Destination *target,
                void *stream, VlessSend send)
{
    *session = (VlessSession){0};
    const size_t prefix_size = 1 + sizeof(settings->uuid) + 1 + 1;
    unsigned char header[1 + sizeof(settings->uuid) + 1 + 1 + V2RAY_ADDRESS_SIZE];
    size_t address_size = write_v2ray_address(header + prefix_size, target);
    if (!address_size)
        return false;
    unsigned char *next = header;

    /* 标准 TCP 请求的附加字段长度为零。 */
    *next++ = VLESS_VERSION;
    memcpy(next, settings->uuid, sizeof(settings->uuid));
    next += sizeof(settings->uuid);
    *next++ = 0;
    *next = VLESS_TCP_CONNECT;

    bool success = send(stream, header, prefix_size + address_size);
    SecureZeroMemory(header, sizeof(header));
    return success;
}

int vless_read(VlessSession *session, void *stream, unsigned char *output, size_t capacity,
               VlessRecv recv)
{
    if (session->response_failed)
        return -1;
    if (!capacity)
        return -2;

    while (!session->response_done) {
        unsigned char addons[UINT8_MAX];
        unsigned char *buffer;
        size_t prefix_size = sizeof(session->response_header);
        size_t remaining;
        if (session->response_used < prefix_size) {
            buffer = session->response_header + session->response_used;
            remaining = prefix_size - session->response_used;
        } else {
            /* 按长度跳过附加字段，避免将其作为代理数据转发。 */
            remaining = prefix_size + session->response_header[1] - session->response_used;
            if (!remaining) {
                session->response_done = true;
                break;
            }
            buffer = addons;
        }

        int received = recv(stream, buffer, remaining);
        if (received == -2)
            return -2;
        if (received <= 0 || (size_t)received > remaining ||
            session->response_header[0] != VLESS_VERSION) {
            session->response_failed = true;
            return -1;
        }
        session->response_used += (size_t)received;
    }

    size_t limit = capacity < INT_MAX ? capacity : INT_MAX;
    return recv(stream, output, limit);
}
