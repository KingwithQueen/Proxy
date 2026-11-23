/* SPDX-License-Identifier: GPL-3.0-or-later */
#ifndef CRAY_VLESS_H
#define CRAY_VLESS_H

#include "core.h"

typedef struct {
    unsigned char response_header[2];
    size_t response_used;
    bool response_done, response_failed;
} VlessSession;

typedef bool (*VlessSend)(void *stream, const void *data, size_t size);
/* 正数为读取的字节数，0 为流结束，-1 为错误，-2 为需要等待。 */
typedef int (*VlessRecv)(void *stream, void *data, size_t size);

/* 初始化会话并发送请求头，后续上传直接写入传输层。 */
bool vless_open(VlessSession *session, const Settings *settings, const Destination *target,
                void *stream, VlessSend send);
/* 仅返回响应数据，跨调用保留尚未读完的响应头。 */
int vless_read(VlessSession *session, void *stream, unsigned char *output, size_t capacity,
               VlessRecv recv);

#endif
