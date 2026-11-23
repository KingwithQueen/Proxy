/* SPDX-License-Identifier: GPL-3.0-or-later */
#ifndef CRAY_ANYTLS_H
#define CRAY_ANYTLS_H

#include "core.h"

enum {
    ANYTLS_WANT_IO = -2,
    ANYTLS_NEEDS_REPLY = -3,
    ANYTLS_WRITE_OVERHEAD = 8192,
    ANYTLS_MAX_RECORDS = 10
};

typedef struct AnyTlsSession AnyTlsSession;
/* 每次回调分别写入一条 TLS 应用数据记录，或者排队等待写入。 */
typedef bool (*AnyTlsSend)(void *stream, const void *data, size_t size);
/* 正数表示已读字节数，0 表示结束，-1 表示错误，-2 表示等待。 */
typedef int (*AnyTlsRecv)(void *stream, void *data, size_t size);

AnyTlsSession *anytls_open(const Settings *settings, const Destination *target,
                          void *stream, AnyTlsSend send);
void anytls_close(AnyTlsSession *session);
/* 等待服务端确认，遇到数据帧时保留帧内容供中继读取。 */
int anytls_await_open(AnyTlsSession *session, AnyTlsRecv recv);
bool anytls_requires_ack(const AnyTlsSession *session);
/* 单次提交不超过 RELAY_BUFFER_SIZE 字节。 */
bool anytls_send(AnyTlsSession *session, const unsigned char *data, size_t size);
bool anytls_finish(AnyTlsSession *session);
int anytls_read(AnyTlsSession *session, unsigned char *output, size_t capacity, AnyTlsRecv recv);
bool anytls_reply(AnyTlsSession *session);

#endif
