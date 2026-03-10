cmake_minimum_required(VERSION 3.10)
project(secureorder_privacy CXX)

# C++ Standard
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Find libsodium via pkg-config
find_package(PkgConfig REQUIRED)
pkg_check_modules(SODIUM REQUIRED libsodium)

# Source Files
set(PRIVACY_SOURCES
    src/privacy/encryption.cpp
    src/privacy/keygen.cpp
    src/privacy/thread_pool.cpp
    src/privacy/batch_decryptor.cpp
)

# libprivacy Static Library
# This is the artifact that Go's CGO links against.
# A static library (.a) is preferred over a shared library (.so/.dylib) for CGO
# because it avoids runtime dynamic linking issues and simplifies deployment.
add_library(privacy STATIC ${PRIVACY_SOURCES})

target_include_directories(privacy
    PUBLIC
        # Exposes include/ to anything that links against libprivacy,
        # including the CGO bridge in Go.
        ${CMAKE_CURRENT_SOURCE_DIR}/include
    PRIVATE
        # libsodium headers (e.g. /opt/homebrew/include on Apple Silicon)
        ${SODIUM_INCLUDE_DIRS}
)

target_link_libraries(privacy
    PRIVATE
        ${SODIUM_LIBRARIES}
)

# Propagate any compile flags pkg-config reports for libsodium
target_compile_options(privacy PRIVATE ${SODIUM_CFLAGS_OTHER})

# Compiler Warnings
target_compile_options(privacy PRIVATE
    -Wall
    -Wextra
    -Wpedantic
    -Wno-unused-parameter
)

# Install Rules
# cmake --install copies the artifacts to a predictable location so the
# Go build can reference them without hardcoded build paths.
include(GNUInstallDirs)

install(TARGETS privacy
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
)

install(DIRECTORY include/privacy/
    DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}/privacy
    FILES_MATCHING PATTERN "*.h"
)

# Tests
# Only build tests when explicitly requested via -DBUILD_TESTS=ON
option(BUILD_TESTS "Build test executables" OFF)

if(BUILD_TESTS)
    enable_testing()

    # Correctness tests
    add_executable(test_encryption
        tests/test_encryption.cpp
    )
    target_include_directories(test_encryption PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/include
        ${SODIUM_INCLUDE_DIRS}
    )
    target_link_libraries(test_encryption PRIVATE
        privacy
        ${SODIUM_LIBRARIES}
    )
    target_compile_options(test_encryption PRIVATE ${SODIUM_CFLAGS_OTHER})

    add_test(NAME encryption_tests COMMAND test_encryption)

    # Benchmarks
    add_executable(bench_decryption
        tests/bench_decryption.cpp
    )
    target_include_directories(bench_decryption PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/include
        ${SODIUM_INCLUDE_DIRS}
    )
    target_link_libraries(bench_decryption PRIVATE
        privacy
        ${SODIUM_LIBRARIES}
    )
    target_compile_options(bench_decryption PRIVATE ${SODIUM_CFLAGS_OTHER})
endif()

# Build Summary
message(STATUS "")
message(STATUS "=== secureorder/cpp build configuration ===")
message(STATUS "  C++ standard   : ${CMAKE_CXX_STANDARD}")
message(STATUS "  libsodium      : ${SODIUM_VERSION} at ${SODIUM_INCLUDE_DIRS}")
message(STATUS "  Install prefix : ${CMAKE_INSTALL_PREFIX}")
message(STATUS "  Build tests    : ${BUILD_TESTS}")
message(STATUS "===========================================")
message(STATUS "")
