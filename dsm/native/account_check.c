// DSM's native identity and expiry bridge. No passwords or SDK structs cross
// the channel. Static Go executors cannot use libc NSS themselves.
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <grp.h>
#include <pwd.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <sys/stat.h>
#include <unistd.h>
#ifndef DSM_SDK_LIBRARY
#define DSM_SDK_LIBRARY "/usr/lib/libsynosdk.so"
#endif
#ifndef DSM_PASSWD_FILE
#define DSM_PASSWD_FILE "/etc/passwd"
#endif
#ifndef DSM_WB_LIBRARY
#define DSM_WB_LIBRARY "/var/packages/SMBService/target/usr/lib/libwbclient.so.0"
#endif

typedef int (*expiry_fn)(const char *, long *);
typedef int (*check_fn)(const char *);
typedef int (*admin_fn)(const char *, int);
typedef int (*ping_fn)(const char *, void **);
typedef void (*free_fn)(void *);
typedef int (*wb_account_fn)(const char *, struct passwd **);

// Classification uses the root-owned local database, never username syntax.
static int local_account(const char *name, uid_t uid) {
    FILE *file = fopen(DSM_PASSWD_FILE, "re");
    struct stat st;
    if (!file || fstat(fileno(file), &st) || !S_ISREG(st.st_mode) || st.st_uid != 0 || (st.st_mode & 022)) {
        if (file) fclose(file);
        return -1;
    }
    struct passwd entry, *out = NULL;
    char buffer[65536];
    int local = 0, rc;
    while ((rc = fgetpwent_r(file, &entry, buffer, sizeof buffer, &out)) == 0 && out) {
        if (!strcmp(entry.pw_name, name)) {
            if (local || entry.pw_uid != uid) { fclose(file); return -1; }
            local = 1;
        }
    }
    fclose(file);
    return rc == ENOENT ? local : -1;
}

// NSS may return cached identities when a DC is down. Require a fresh DC ping
// for a directory account. Unsupported providers and all unknowns refuse.
static int directory_online(const char *canonical, uid_t uid, gid_t gid) {
    void *library = dlopen(DSM_WB_LIBRARY, RTLD_NOW | RTLD_LOCAL);
    if (!library) return 0;
    ping_fn ping = (ping_fn)dlsym(library, "wbcPingDc");
    free_fn release = (free_fn)dlsym(library, "wbcFreeMemory");
    wb_account_fn lookup = (wb_account_fn)dlsym(library, "wbcGetpwnam");
    if (!ping || !release || !lookup) { dlclose(library); return 0; }
    // Prove the provider: a canonical-looking NSS name alone is insufficient.
    struct passwd *account = NULL;
    int result = 0;
    if (lookup(canonical, &account) != 0 || !account || !account->pw_name ||
        strcasecmp(account->pw_name, canonical) || account->pw_uid != uid || account->pw_gid != gid) goto end;
    const char *separator = strchr(account->pw_name, '\\');
    if (!separator || separator == account->pw_name || separator - account->pw_name > 255) goto end;
    char domain[256];
    memcpy(domain, account->pw_name, (size_t)(separator - account->pw_name));
    domain[separator - account->pw_name] = '\0';
    void *error = NULL;
    int rc = ping(domain, &error);
    if (error) release(error);
    result = rc == 0;
end:
    if (account) release(account);
    dlclose(library);
    return result;
}

static void json_string(const char *s) {
    putchar('"');
    for (const unsigned char *p = (const unsigned char *)s; *p; ++p) {
        if (*p == '"' || *p == '\\') { putchar('\\'); putchar(*p); }
        else if (*p < 32) printf("\\u%04x", *p);
        else putchar(*p);
    }
    putchar('"');
}

int main(int argc, char **argv) {
    if (argc != 2 || !argv[1][0] || strlen(argv[1]) > 1024) return 2;
    struct passwd first, reverse, last, *p = NULL;
    char a[65536], b[65536], c[65536];
    if (getpwnam_r(argv[1], &first, a, sizeof a, &p) || !p || first.pw_uid < 1024) return 2;
    uid_t uid = first.pw_uid;
    gid_t gid = first.pw_gid;
    int local = local_account(argv[1], uid);
    if (local < 0 || (local ? strcmp(first.pw_name, argv[1]) : strcasecmp(first.pw_name, argv[1])) || (!local && !directory_online(first.pw_name, uid, gid))) return 2;
    if (getpwuid_r(uid, &reverse, b, sizeof b, &p) || !p || strcmp(reverse.pw_name, first.pw_name) || reverse.pw_gid != gid) return 2;
    // AD NSS returns canonical display casing for reverse lookup. Require
    // a second forward lookup of that canonical name to bind it to this UID.
    struct passwd canonical;
    char canonical_buffer[65536];
    if (getpwnam_r(reverse.pw_name, &canonical, canonical_buffer, sizeof canonical_buffer, &p) || !p || canonical.pw_uid != uid || canonical.pw_gid != gid) return 2;
    void *library = dlopen(DSM_SDK_LIBRARY, RTLD_NOW | RTLD_LOCAL);
    if (!library) return 2;
    expiry_fn day_get = (expiry_fn)dlsym(library, "SLIBUserExpiryDayGet");
    check_fn check = (check_fn)dlsym(library, "SYNOUserCheckExpired");
    admin_fn admin_get = (admin_fn)dlsym(library, "SLIBGroupIsAdminGroupMem");
    if (!day_get || !check || !admin_get) { dlclose(library); return 2; }
    long day = 0;
    int rc = day_get(argv[1], &day);
    int expired = check(argv[1]);
    int admin = admin_get(argv[1], 1); // bypass directory-admin cache
    dlclose(library);
    if (rc != 0 || (expired != 0 && expired != 1) || (admin != 0 && admin != 1)) return 2;
    gid_t groups[1024];
    int count = 1024;
    if (getgrouplist(argv[1], gid, groups, &count) < 0 || count < 1 || count > 1024) return 2;
    // Recheck after expiry/group queries: rename, removal or UID reuse refuses.
    if (getpwnam_r(argv[1], &last, c, sizeof c, &p) || !p || strcmp(last.pw_name, first.pw_name) || last.pw_uid != uid || last.pw_gid != gid) return 2;
    printf("{\"username\":"); json_string(argv[1]);
    printf(",\"uid\":%lu,\"gid\":%lu,\"home\":", (unsigned long)uid, (unsigned long)gid); json_string(last.pw_dir);
    printf(",\"admin\":%s,\"expired\":\"%s\",\"groups\":[", admin ? "true" : "false", day == -1 && expired == 0 ? "normal" : "now");
    for (int i = 0; i < count; ++i) printf("%s%lu", i ? "," : "", (unsigned long)groups[i]);
    return printf("]}\n") > 0 && !ferror(stdout) ? 0 : 2;
}
