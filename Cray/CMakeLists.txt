# SPDX-License-Identifier: GPL-3.0-or-later
cmake_minimum_required(VERSION 3.20)
project(Cray VERSION 1.0.0 LANGUAGES C)

if(NOT WIN32)
    message(FATAL_ERROR "Cray targets Windows.")
endif()

set(MBEDTLS_SOURCE_DIR "" CACHE PATH "Path to unpacked Mbed TLS 3.6 LTS sources")
if(NOT EXISTS "${MBEDTLS_SOURCE_DIR}/include/mbedtls/ssl.h")
    message(FATAL_ERROR "Set MBEDTLS_SOURCE_DIR to the Mbed TLS source directory.")
endif()

set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
add_compile_definitions(WINVER=0x0601 _WIN32_WINNT=0x0601 WIN32_LEAN_AND_MEAN)
set(ENABLE_PROGRAMS OFF CACHE BOOL "" FORCE)
set(ENABLE_TESTING OFF CACHE BOOL "" FORCE)
set(USE_SHARED_MBEDTLS_LIBRARY OFF CACHE BOOL "" FORCE)
set(USE_STATIC_MBEDTLS_LIBRARY ON CACHE BOOL "" FORCE)
set(MBEDTLS_FATAL_WARNINGS OFF CACHE BOOL "" FORCE)
add_subdirectory("${MBEDTLS_SOURCE_DIR}" "${CMAKE_CURRENT_BINARY_DIR}/mbedtls")
target_include_directories(mbedcrypto PUBLIC "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>")
target_compile_definitions(mbedcrypto PUBLIC MBEDTLS_USER_CONFIG_FILE="mbedtls_user_config.h")

add_executable(Cray main.c config.c proxy.c tls.c vmess.c vless.c)
target_compile_features(Cray PRIVATE c_std_11)
target_link_libraries(Cray PRIVATE mbedtls mbedx509 mbedcrypto ws2_32 crypt32 bcrypt)
if(MSVC)
    target_compile_options(Cray PRIVATE /W4 /utf-8)
    target_link_options(Cray PRIVATE /SUBSYSTEM:CONSOLE,6.01)
elseif(MINGW)
    target_compile_options(Cray PRIVATE -Wall -Wextra -Wpedantic)
    target_link_options(Cray PRIVATE -municode -static
        -Wl,--major-subsystem-version,6,--minor-subsystem-version,1
        -Wl,--major-os-version,6,--minor-os-version,1)
endif()
