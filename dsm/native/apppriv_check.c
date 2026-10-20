// Minimal DSM SDK bridge for Application Privileges. DSM evaluates its own
// user, group and everyone rules, allow/deny precedence and IP ranges; this
// bridge only passes the verified principal, the registered app ID and the
// client address, and prints DSM's exact decision.
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

#ifndef DSM_SDK_LIBRARY
#define DSM_SDK_LIBRARY "/usr/lib/libsynosdk.so"
#endif

// ABI measured on DSM 7.4 (nas.example, kernel 4.4): int (const char *user,
// const char *app, const char *ip) returning 1 (allowed) or 0 (refused).
// A NULL address refuses every account, so the address is mandatory.
typedef int (*apppriv_check_fn)(const char *, const char *, const char *);
_Static_assert(sizeof(int) == 4, "DSM BOOL ABI requires a 32-bit int");

static int bounded(const char *value, size_t max) {
    return value[0] != '\0' && strlen(value) <= max;
}

int main(int argc, char **argv) {
    if (argc != 4 || !bounded(argv[1], 1024) || !bounded(argv[2], 128) || !bounded(argv[3], 64)) return 2;
    void *library = dlopen(DSM_SDK_LIBRARY, RTLD_NOW | RTLD_LOCAL);
    if (!library) return 2;
    apppriv_check_fn check = (apppriv_check_fn)dlsym(library, "SLIBAppPrivUserHas");
    if (!check) { dlclose(library); return 2; }
    int result = check(argv[1], argv[2], argv[3]);
    dlclose(library);
    if (result != 0 && result != 1) return 2;
    return printf("%d\n", result) == 2 ? 0 : 2;
}
