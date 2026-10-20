// Minimal DSM SDK bridge: the Go CGI remains static. No group names, UID
// shortcuts, environment-based library selection or application policy here.
#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

#ifndef DSM_SDK_LIBRARY
#define DSM_SDK_LIBRARY "/usr/lib/libsynosdk.so"
#endif

// ABI from synosdk/group_p.h: int (const char *, BOOL), BOOL is a 32-bit int.
// Keep the bridge limited to this primitive ABI instead of copying SDK structs.
typedef int (*admin_check_fn)(const char *, int);
_Static_assert(sizeof(int) == 4, "DSM BOOL ABI requires a 32-bit int");

int main(int argc, char **argv) {
    if (argc != 2 || argv[1][0] == '\0' || strlen(argv[1]) > 1024) return 2;
    void *library = dlopen(DSM_SDK_LIBRARY, RTLD_NOW | RTLD_LOCAL);
    if (!library) return 2;
    admin_check_fn check = (admin_check_fn)dlsym(library, "SLIBGroupIsAdminGroupMem");
    if (!check) { dlclose(library); return 2; }
    // TRUE bypasses DSM's directory-admin cache; DSM resolves local/domain/LDAP
    // identity and configured administrator groups itself.
    int result = check(argv[1], 1);
    dlclose(library);
    if (result != 0 && result != 1) return 2;
    return printf("%d\n", result) == 2 ? 0 : 2;
}
