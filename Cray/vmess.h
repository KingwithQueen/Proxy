/* SPDX-License-Identifier: GPL-3.0-or-later */
#ifndef CRAY_VMESS_H
#define CRAY_VMESS_H

#include "core.h"

/* 一次提交至多 RELAY_BUFFER_SIZE 字节时，分块、标签和填充所需的额外空间。 */
enum { VMESS_WRITE_OVERHEAD = 2 * (2 + 16 + 63) };

typedef struct VmessSession VmessSession;
typedef bool (*VmessSend)(void *stream, const void *data, size_t size);
/* 正数为读取的字节数，0 为流结束，-1 为错误，-2 为需要等待。 */
typedef int (*VmessRecv)(void *stream, void *data, size_t size);

VmessSession *vmess_open(const Settings *settings, const Destination *target,
                         void *stream, VmessSend send);
void vmess_close(VmessSession *session);
bool vmess_send(VmessSession *session, const unsigned char *data, size_t size);
/* 发送分块结束标记，由调用方管理传输层半关闭。 */
bool vmess_finish(VmessSession *session);
int vmess_read(VmessSession *session, unsigned char *output, size_t capacity, VmessRecv recv);

#endif
