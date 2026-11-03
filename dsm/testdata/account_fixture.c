// Synthetic NSS and SDK boundaries; no DSM headers, libraries or source.
#define getpwnam_r fixture_getpwnam_r
#define getpwuid_r fixture_getpwuid_r
#define getgrouplist fixture_getgrouplist
#define dlopen fixture_dlopen
#define dlsym fixture_dlsym
#define dlclose fixture_dlclose
#include "../native/account_check.c"

static const char *requested;
static int forwards;

int fixture_getpwnam_r(const char *name, struct passwd *pw, char *buffer, size_t size, struct passwd **result) {
    (void)buffer; (void)size;
    if (!requested) requested = name;
    *result = NULL;
    if (strstr(name, "missing")) return 0;
    if (strstr(name, "ioerror")) return EAGAIN;
    pw->pw_name = (char *)name;
    pw->pw_uid = 2969567732U;
    pw->pw_gid = 2969567745U;
    pw->pw_dir = "/home/alice";
    if (++forwards == 3 && strstr(name, "changed")) pw->pw_uid++;
    *result = pw;
    return 0;
}

int fixture_getpwuid_r(uid_t uid, struct passwd *pw, char *buffer, size_t size, struct passwd **result) {
    (void)uid;
    int rc = fixture_getpwnam_r(requested, pw, buffer, size, result);
    --forwards;
    return rc;
}

int fixture_getgrouplist(const char *name, gid_t gid, gid_t *groups, int *count) {
    (void)name;
    if (*count < 2) return -1;
    groups[0] = gid; groups[1] = 101; *count = 2;
    return 2;
}

static int fixture_expiry(const char *name, long *day) {
    (void)name; *day = -1; return 0;
}
static int fixture_check(const char *name) { (void)name; return 0; }
static int fixture_admin(const char *name, int fresh) { (void)name; return fresh; }
static int fixture_ping(const char *domain, void **error) {
    (void)domain; *error = NULL;
    return strstr(requested, "offline") ? 1 : 0;
}
static int fixture_wb(const char *name, struct passwd **out) {
    static struct passwd pw;
    if (strchr(name, '@')) return 1;
    pw.pw_name = (char *)name; pw.pw_uid = 2969567732U; pw.pw_gid = 2969567745U;
    *out = &pw;
    return 0;
}
static void fixture_free(void *ptr) { (void)ptr; }
void *fixture_dlopen(const char *path, int flags) {
    (void)path; (void)flags; return (void *)1;
}
int fixture_dlclose(void *handle) { (void)handle; return 0; }
void *fixture_dlsym(void *handle, const char *name) {
    (void)handle;
    if (!strcmp(name, "wbcPingDc")) return (void *)fixture_ping;
    if (!strcmp(name, "wbcGetpwnam")) return (void *)fixture_wb;
    if (!strcmp(name, "wbcFreeMemory")) return (void *)fixture_free;
    if (!strcmp(name, "SLIBUserExpiryDayGet")) return (void *)fixture_expiry;
    if (!strcmp(name, "SYNOUserCheckExpired")) return (void *)fixture_check;
    if (!strcmp(name, "SLIBGroupIsAdminGroupMem")) return (void *)fixture_admin;
    return NULL;
}
