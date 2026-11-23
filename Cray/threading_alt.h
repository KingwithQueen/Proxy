/* SPDX-License-Identifier: GPL-3.0-or-later */
#ifndef TROJAN_MBEDTLS_THREADING_ALT_H
#define TROJAN_MBEDTLS_THREADING_ALT_H

#include <windows.h>

typedef struct {
    SRWLOCK lock;
} mbedtls_threading_mutex_t;

#endif
