/* SPDX-License-Identifier: GPL-3.0-or-later */
#include "anytls.h"

#include <bcrypt.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "mbedtls/md5.h"

enum {
    FRAME_HEADER_SIZE = 7,
    TLS_RECORD_SIZE = 16384,
    PASSWORD_HASH_SIZE = 32,
    AUTH_HEADER_SIZE = PASSWORD_HASH_SIZE + 2,
    PADDING_RANGES = ANYTLS_MAX_RECORDS - 2,
    PADDING_ITEMS = 2 * PADDING_RANGES + 1,
    MAX_PADDING_SCHEME_SIZE = 8192,
    MAX_PADDING_RULES = 128,
    STREAM_ID = 1
};

_Static_assert(RELAY_BUFFER_SIZE <= UINT16_MAX &&
    FRAME_HEADER_SIZE + RELAY_BUFFER_SIZE <= 2 * TLS_RECORD_SIZE,
    "AnyTLS upload exceeds its frame or record limit");
_Static_assert(AUTH_HEADER_SIZE + ANYTLS_WRITE_OVERHEAD <= TLS_RECORD_SIZE,
    "AnyTLS authentication must fit in one TLS record");

typedef enum {
    CMD_WASTE = 0,
    CMD_SYN = 1,
    CMD_PSH = 2,
    CMD_FIN = 3,
    CMD_SETTINGS = 4,
    CMD_ALERT = 5,
    CMD_PADDING_UPDATE = 6,
    CMD_SYN_ACK = 7,
    CMD_HEART_REQUEST = 8,
    CMD_HEART_RESPONSE = 9,
    CMD_SERVER_SETTINGS = 10
} Command;

typedef struct {
    uint16_t minimum, maximum;
} PaddingRange;

typedef struct {
    uint32_t packet;
    size_t count;
    PaddingRange ranges[PADDING_ITEMS];
} PaddingRule;

typedef struct {
    volatile LONG references;
    uint32_t stop;
    size_t rule_count;
    char md5[33];
    PaddingRule rules[];
} PaddingScheme;

struct AnyTlsSession {
    void *stream;
    AnyTlsSend send;
    PaddingScheme *padding;
    uint32_t packet;
    unsigned char header[FRAME_HEADER_SIZE];
    size_t header_used, frame_size, frame_used;
    uint32_t frame_stream, heartbeat_stream;
    unsigned char *control;
    bool heartbeat_pending, server_v2, acknowledged, send_closed, received_fin, failed;
};

/* 每个 Cray 进程只有一个出站。已有连接各自持有填充方案的引用。 */
static SRWLOCK padding_lock = SRWLOCK_INIT;
static PaddingScheme *latest_padding;

static const unsigned char default_padding[] =
    "stop=8\n"
    "0=30-30\n"
    "1=100-400\n"
    "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000\n"
    "3=9-9,500-1000\n"
    "4=500-1000\n"
    "5=500-1000\n"
    "6=500-1000\n"
    "7=500-1000";

static void write_uint16(unsigned char *output, uint16_t number)
{
    output[0] = (unsigned char)(number >> 8);
    output[1] = (unsigned char)number;
}

static void write_frame_header(unsigned char *output, Command command, uint32_t stream, uint16_t size)
{
    output[0] = (unsigned char)command;
    write_uint16(output + 1, (uint16_t)(stream >> 16));
    write_uint16(output + 3, (uint16_t)stream);
    write_uint16(output + 5, size);
}

static bool read_decimal(const unsigned char **cursor, const unsigned char *end, uint32_t *value)
{
    const unsigned char *next = *cursor;
    uint32_t number = 0;
    while (next < end && *next >= '0' && *next <= '9') {
        uint32_t digit = (uint32_t)(*next++ - '0');
        if (number > (UINT32_MAX - digit) / 10)
            return false;
        number = number * 10 + digit;
    }
    if (next == *cursor)
        return false;
    *cursor = next;
    *value = number;
    return true;
}

static bool read_padding_rule(const unsigned char *cursor, const unsigned char *end, PaddingRule *rule)
{
    size_t total = FRAME_HEADER_SIZE;
    size_t ranges = 0;
    while (cursor < end && rule->count < PADDING_ITEMS) {
        PaddingRange *range = &rule->ranges[rule->count++];
        if (*cursor == 'c') {
            cursor++;
        } else {
            uint32_t first, second;
            if (!read_decimal(&cursor, end, &first) || cursor == end || *cursor++ != '-' ||
                !read_decimal(&cursor, end, &second) || !first || !second)
                return false;
            uint32_t upper = first > second ? first : second;
            if (++ranges > PADDING_RANGES || upper > ANYTLS_WRITE_OVERHEAD - FRAME_HEADER_SIZE)
                return false;
            /* 即使策略要求发送纯填充，本次提交也必须能放入中继发送队列。 */
            total += FRAME_HEADER_SIZE + upper;
            if (total > ANYTLS_WRITE_OVERHEAD)
                return false;
            range->minimum = (uint16_t)(first < second ? first : second);
            range->maximum = (uint16_t)upper;
        }
        if (cursor == end)
            return ranges > 0;
        if (*cursor++ != ',')
            return false;
    }
    return false;
}

static int compare_padding_rules(const void *left, const void *right)
{
    uint32_t a = ((const PaddingRule *)left)->packet;
    uint32_t b = ((const PaddingRule *)right)->packet;
    return (a > b) - (a < b);
}

static PaddingScheme *parse_padding(const unsigned char *text, size_t size)
{
    static const char hex[] = "0123456789abcdef";
    if (!size || size > MAX_PADDING_SCHEME_SIZE)
        return NULL;
    size_t line_count = 0;
    for (size_t i = 0; i < size; i++)
        if (text[i] == '=' && ++line_count > MAX_PADDING_RULES + 1)
            return NULL;

    PaddingScheme *scheme = calloc(1, sizeof(*scheme) + line_count * sizeof(*scheme->rules));
    if (!scheme)
        return NULL;
    scheme->references = 1;
    bool have_stop = false;
    const unsigned char *end = text + size;
    const unsigned char *cursor = text;
    while (cursor < end) {
        const unsigned char *newline = memchr(cursor, '\n', (size_t)(end - cursor));
        const unsigned char *line_start = cursor;
        const unsigned char *line_end = newline ? newline : end;
        cursor = newline ? newline + 1 : end;
        if (line_end > line_start && line_end[-1] == '\r')
            line_end--;
        if (line_start == line_end)
            continue;
        if (line_end - line_start >= 5 && memcmp(line_start, "stop=", 5) == 0) {
            const unsigned char *value = line_start + 5;
            if (have_stop || !read_decimal(&value, line_end, &scheme->stop) || value != line_end)
                goto invalid;
            have_stop = true;
        } else {
            uint32_t packet;
            if (!read_decimal(&line_start, line_end, &packet) || line_start == line_end || *line_start++ != '=')
                goto invalid;
            PaddingRule *rule = &scheme->rules[scheme->rule_count++];
            rule->packet = packet;
            if (!read_padding_rule(line_start, line_end, rule))
                goto invalid;
        }
    }
    if (!have_stop)
        goto invalid;
    qsort(scheme->rules, scheme->rule_count, sizeof(*scheme->rules), compare_padding_rules);
    for (size_t i = 1; i < scheme->rule_count; i++)
        if (scheme->rules[i - 1].packet == scheme->rules[i].packet)
            goto invalid;

    unsigned char digest[16];
    if (mbedtls_md5(text, size, digest) != 0)
        goto invalid;
    for (size_t i = 0; i < sizeof(digest); i++) {
        scheme->md5[2 * i] = hex[digest[i] >> 4];
        scheme->md5[2 * i + 1] = hex[digest[i] & 15];
    }
    return scheme;

invalid:
    free(scheme);
    return NULL;
}

static void release_padding(PaddingScheme *scheme)
{
    if (scheme && InterlockedDecrement(&scheme->references) == 0)
        free(scheme);
}

static PaddingScheme *get_padding(void)
{
    AcquireSRWLockExclusive(&padding_lock);
    if (!latest_padding)
        latest_padding = parse_padding(default_padding, sizeof(default_padding) - 1);
    PaddingScheme *scheme = latest_padding;
    if (scheme)
        InterlockedIncrement(&scheme->references);
    ReleaseSRWLockExclusive(&padding_lock);
    return scheme;
}

static void update_padding(AnyTlsSession *session)
{
    PaddingScheme *next = parse_padding(session->control, session->frame_size);
    if (!next)
        return;
    AcquireSRWLockExclusive(&padding_lock);
    PaddingScheme *previous = latest_padding;
    latest_padding = next;
    InterlockedIncrement(&next->references);
    ReleaseSRWLockExclusive(&padding_lock);
    release_padding(previous);
    release_padding(session->padding);
    session->padding = next;
}

static const PaddingRule *find_padding_rule(const PaddingScheme *scheme, uint32_t packet)
{
    PaddingRule key = {0};
    key.packet = packet;
    return bsearch(&key, scheme->rules, scheme->rule_count, sizeof(*scheme->rules), compare_padding_rules);
}

static bool choose_padding(PaddingRange range, size_t *size)
{
    uint32_t width = (uint32_t)(range.maximum - range.minimum);
    uint32_t random = 0;
    if (width) {
        uint32_t threshold = (0u - width) % width;
        do {
            if (!BCRYPT_SUCCESS(BCryptGenRandom(NULL, (unsigned char *)&random, sizeof(random),
                                               BCRYPT_USE_SYSTEM_PREFERRED_RNG)))
                return false;
        } while (random < threshold);
        random %= width;
    }
    *size = range.minimum + random;
    return true;
}

static void write_waste(unsigned char *output, size_t size)
{
    write_frame_header(output, CMD_WASTE, 0, (uint16_t)size);
    memset(output + FRAME_HEADER_SIZE, 0, size);
}

static bool send_records(AnyTlsSession *session, const unsigned char *data, size_t size)
{
    if (session->packet < UINT32_MAX)
        session->packet++;
    const PaddingRule *rule = session->packet < session->padding->stop ?
        find_padding_rule(session->padding, session->packet) : NULL;
    unsigned char buffer[ANYTLS_WRITE_OVERHEAD];

    if (rule) {
        for (size_t i = 0; i < rule->count; i++) {
            PaddingRange range = rule->ranges[i];
            if (!range.minimum) {
                if (!size)
                    break;
                continue;
            }
            size_t record_size;
            if (!choose_padding(range, &record_size))
                return false;
            if (size > record_size) {
                if (!session->send(session->stream, data, record_size))
                    return false;
                size -= record_size;
                data += record_size;
            } else if (!size) {
                write_waste(buffer, record_size);
                if (!session->send(session->stream, buffer, FRAME_HEADER_SIZE + record_size))
                    return false;
            } else {
                if (record_size - size > FRAME_HEADER_SIZE) {
                    memcpy(buffer, data, size);
                    write_waste(buffer + size, record_size - size - FRAME_HEADER_SIZE);
                    if (!session->send(session->stream, buffer, record_size))
                        return false;
                } else if (!session->send(session->stream, data, size)) {
                    return false;
                }
                size = 0;
            }
        }
    }

    /* 一个代理帧可以跨 TLS 记录，但每次回调只写一条 TLS 记录。 */
    while (size) {
        size_t record_size = size < TLS_RECORD_SIZE ? size : TLS_RECORD_SIZE;
        if (!session->send(session->stream, data, record_size))
            return false;
        size -= record_size;
        data += record_size;
    }
    return true;
}

static bool send_auth(AnyTlsSession *session, const unsigned char password_hash[PASSWORD_HASH_SIZE])
{
    unsigned char message[AUTH_HEADER_SIZE + ANYTLS_WRITE_OVERHEAD];
    const PaddingRule *rule = find_padding_rule(session->padding, 0);
    size_t size = 0;
    if (rule && !choose_padding(rule->ranges[0], &size))
        return false;

    /* 参考服务端只读一次首条 TLS 应用数据，认证和填充必须一起写入。 */
    memcpy(message, password_hash, PASSWORD_HASH_SIZE);
    write_uint16(message + PASSWORD_HASH_SIZE, (uint16_t)size);
    memset(message + AUTH_HEADER_SIZE, 0, size);
    bool sent = session->send(session->stream, message, AUTH_HEADER_SIZE + size);
    SecureZeroMemory(message, AUTH_HEADER_SIZE);
    return sent;
}

static bool send_open_frames(AnyTlsSession *session, const Destination *target)
{
    char settings[80];
    int length = snprintf(settings, sizeof(settings), "v=2\nclient=Cray/1.0.0\npadding-md5=%s",
                          session->padding->md5);
    if (length < 0 || (size_t)length >= sizeof(settings))
        return false;

    /* 首个会话数据包同时承载设置、开流和 SOCKS 目标地址。 */
    unsigned char frames[3 * FRAME_HEADER_SIZE + sizeof(settings) + SOCKS_ADDRESS_SIZE];
    write_frame_header(frames, CMD_SETTINGS, 0, (uint16_t)length);
    memcpy(frames + FRAME_HEADER_SIZE, settings, (size_t)length);
    unsigned char *next = frames + FRAME_HEADER_SIZE + length;
    write_frame_header(next, CMD_SYN, STREAM_ID, 0);
    next += FRAME_HEADER_SIZE;
    size_t address_size = write_socks_address(next + FRAME_HEADER_SIZE, target);
    if (!address_size)
        return false;
    write_frame_header(next, CMD_PSH, STREAM_ID, (uint16_t)address_size);
    return send_records(session, frames, (size_t)(next - frames) + FRAME_HEADER_SIZE + address_size);
}

AnyTlsSession *anytls_open(const Settings *settings, const Destination *target,
                          void *stream, AnyTlsSend send)
{
    AnyTlsSession *session = calloc(1, sizeof(*session));
    if (!session)
        return NULL;
    session->stream = stream;
    session->send = send;
    session->padding = get_padding();
    if (session->padding && send_auth(session, settings->anytls_password_hash) &&
        send_open_frames(session, target))
        return session;
    anytls_close(session);
    return NULL;
}

void anytls_close(AnyTlsSession *session)
{
    if (!session)
        return;
    release_padding(session->padding);
    free(session->control);
    free(session);
}

bool anytls_send(AnyTlsSession *session, const unsigned char *data, size_t size)
{
    if (session->send_closed || session->received_fin || session->failed || size > RELAY_BUFFER_SIZE)
        return false;
    if (!size)
        return true;
    unsigned char frame[FRAME_HEADER_SIZE + RELAY_BUFFER_SIZE];
    write_frame_header(frame, CMD_PSH, STREAM_ID, (uint16_t)size);
    memcpy(frame + FRAME_HEADER_SIZE, data, size);
    session->failed = !send_records(session, frame, FRAME_HEADER_SIZE + size);
    return !session->failed;
}

bool anytls_finish(AnyTlsSession *session)
{
    if (session->failed)
        return false;
    if (session->send_closed || session->received_fin)
        return true;

    unsigned char frame[FRAME_HEADER_SIZE];
    write_frame_header(frame, CMD_FIN, STREAM_ID, 0);
    session->send_closed = send_records(session, frame, sizeof(frame));
    session->failed = !session->send_closed;
    return session->send_closed;
}

bool anytls_reply(AnyTlsSession *session)
{
    if (session->failed)
        return false;
    if (!session->heartbeat_pending)
        return true;

    unsigned char frame[FRAME_HEADER_SIZE];
    write_frame_header(frame, CMD_HEART_RESPONSE, session->heartbeat_stream, 0);
    session->failed = !send_records(session, frame, sizeof(frame));
    session->heartbeat_pending = false;
    return !session->failed;
}

static int receive_bytes(AnyTlsSession *session, AnyTlsRecv recv, void *output, size_t size)
{
    int length = recv(session->stream, output, size);
    if (length < ANYTLS_WANT_IO || (length > 0 && (size_t)length > size))
        return -1;
    return length;
}

static int receive_frame_header(AnyTlsSession *session, AnyTlsRecv recv)
{
    if (session->header_used == FRAME_HEADER_SIZE)
        return 1;

    while (session->header_used < FRAME_HEADER_SIZE) {
        int length = receive_bytes(session, recv, session->header + session->header_used,
                                   FRAME_HEADER_SIZE - session->header_used);
        if (length <= 0)
            return !length && session->header_used ? -1 : length;
        session->header_used += (size_t)length;
    }
    const unsigned char *header = session->header;
    session->frame_stream = (uint32_t)header[1] << 24 | (uint32_t)header[2] << 16 |
                            (uint32_t)header[3] << 8 | header[4];
    session->frame_size = (size_t)header[5] << 8 | header[6];
    return 1;
}

static int receive_control(AnyTlsSession *session, AnyTlsRecv recv, bool save)
{
    unsigned char discard[1024];
    if (save && session->frame_size && !session->control) {
        if (session->frame_used)
            return -1;
        session->control = calloc(session->frame_size, 1);
        if (!session->control)
            return -1;
    }
    while (session->frame_used < session->frame_size) {
        size_t remaining = session->frame_size - session->frame_used;
        unsigned char *output = session->control ? session->control + session->frame_used : discard;
        if (!session->control && remaining > sizeof(discard))
            remaining = sizeof(discard);
        int length = receive_bytes(session, recv, output, remaining);
        if (length <= 0)
            return length == ANYTLS_WANT_IO ? ANYTLS_WANT_IO : -1;
        session->frame_used += (size_t)length;
    }
    return 1;
}

static void reset_frame(AnyTlsSession *session)
{
    session->header_used = 0;
    session->frame_used = 0;
    free(session->control);
    session->control = NULL;
}

static bool settings_have_ack(const unsigned char *text, size_t size)
{
    const unsigned char *end = text + size;
    while (text < end) {
        const unsigned char *newline = memchr(text, '\n', (size_t)(end - text));
        const unsigned char *line_end = newline ? newline : end;
        if (line_end > text && line_end[-1] == '\r')
            line_end--;
        if (line_end - text >= 3 && memcmp(text, "v=", 2) == 0) {
            uint32_t version;
            const unsigned char *value = text + 2;
            if (read_decimal(&value, line_end, &version) && value == line_end && version >= 2)
                return true;
        }
        text = newline ? newline + 1 : end;
    }
    return false;
}

static void log_peer_error(const char *reason, const AnyTlsSession *session)
{
    /* 过滤控制字符，限制服务端错误文本的日志长度。 */
    char text[161];
    size_t size = session->frame_size < sizeof(text) - 1 ? session->frame_size : sizeof(text) - 1;
    for (size_t i = 0; i < size; i++) {
        unsigned char byte = session->control[i];
        text[i] = byte < 32 || byte == 127 ? '?' : (char)byte;
    }
    text[size] = '\0';
    fprintf(stderr, "Cray: AnyTLS %s%s%s\n", reason, size ? ": " : "", text);
}

static void handle_control(AnyTlsSession *session, Command command)
{
    switch (command) {
    case CMD_ALERT:
        log_peer_error("server alert", session);
        session->failed = true;
        break;
    case CMD_PADDING_UPDATE:
        if (session->control)
            update_padding(session);
        break;
    case CMD_SERVER_SETTINGS:
        if (session->frame_size && settings_have_ack(session->control, session->frame_size))
            session->server_v2 = true;
        break;
    case CMD_SYN_ACK:
        if (session->frame_stream == STREAM_ID) {
            if (session->frame_size) {
                log_peer_error("stream rejected", session);
                session->failed = true;
            } else {
                session->acknowledged = true;
            }
        }
        break;
    case CMD_HEART_REQUEST:
        session->heartbeat_pending = true;
        session->heartbeat_stream = session->frame_stream;
        break;
    case CMD_FIN:
        if (session->frame_stream == STREAM_ID)
            session->received_fin = true;
        break;
    default:
        break;
    }
}

static int read_stream(AnyTlsSession *session, unsigned char *output, size_t capacity,
                       AnyTlsRecv recv, bool opening)
{
    for (;;) {
        if (session->failed)
            return -1;
        if (session->received_fin)
            return opening ? -1 : 0;
        if (session->heartbeat_pending)
            return ANYTLS_NEEDS_REPLY;
        if (opening && session->acknowledged)
            return 1;

        int result = receive_frame_header(session, recv);
        if (result == ANYTLS_WANT_IO)
            return ANYTLS_WANT_IO;
        if (result <= 0) {
            if (!result && !opening) {
                session->received_fin = true;
                return 0;
            }
            break;
        }

        Command command = (Command)session->header[0];
        if (command == CMD_PSH && session->frame_stream == STREAM_ID &&
            session->frame_used < session->frame_size) {
            if (opening)
                return 1;
            size_t remaining = session->frame_size - session->frame_used;
            if (remaining > capacity)
                remaining = capacity;
            result = receive_bytes(session, recv, output, remaining);
            if (result == ANYTLS_WANT_IO)
                return ANYTLS_WANT_IO;
            if (result <= 0)
                break;
            session->frame_used += (size_t)result;
            if (session->frame_used == session->frame_size)
                reset_frame(session);
            return result;
        }

        if ((command == CMD_FIN || command == CMD_HEART_REQUEST || command == CMD_HEART_RESPONSE) &&
            session->frame_size)
            break;
        bool save = command == CMD_ALERT ||
                    (command == CMD_PADDING_UPDATE && session->frame_size <= MAX_PADDING_SCHEME_SIZE) ||
                    command == CMD_SERVER_SETTINGS ||
                    (command == CMD_SYN_ACK && session->frame_stream == STREAM_ID);
        result = receive_control(session, recv, save);
        if (result == ANYTLS_WANT_IO)
            return ANYTLS_WANT_IO;
        if (result != 1)
            break;
        handle_control(session, command);
        reset_frame(session);
    }
    session->failed = true;
    return -1;
}

int anytls_await_open(AnyTlsSession *session, AnyTlsRecv recv)
{
    return read_stream(session, NULL, 0, recv, true);
}

bool anytls_requires_ack(const AnyTlsSession *session)
{
    return session->server_v2;
}

int anytls_read(AnyTlsSession *session, unsigned char *output, size_t capacity, AnyTlsRecv recv)
{
    if (!capacity)
        return ANYTLS_WANT_IO;
    return read_stream(session, output, capacity, recv, false);
}
