import { execFile } from 'node:child_process';
import { join, isAbsolute } from 'node:path';
import { AppError } from './errors.js';
/** Installer-selected folder, checked by the SDK controller as this package UID.
 * Configure this once in trusted App code. No DSM session or ACL writer is used.
 */
export class SharedFolderConfig {
    options;
    root;
    data;
    constructor(options) {
        this.options = options;
        if (!/^[A-Za-z][A-Za-z0-9]{1,63}$/.test(options.packageId))
            throw new TypeError('invalid package ID');
        this.root = options.payloadRoot ?? `/var/packages/${options.packageId}/target`;
        this.data = options.dataDir ?? `/var/packages/${options.packageId}/var`;
        if (!isAbsolute(this.root) || !isAbsolute(this.data))
            throw new TypeError('package paths must be absolute');
    }
    /** Re-resolve volume + share + subpath and require the declared RO/RW access. */
    require(key) {
        if (!/^[a-z][a-z0-9_]{0,31}$/.test(key))
            return Promise.reject(new TypeError('invalid folder key'));
        return new Promise((resolve, reject) => {
            execFile(join(this.root, 'install/control'), ['folder-path', '--root', this.root, '--package', this.options.packageId, '--key', key], {
                env: { ...process.env, SYNOPKG_PKGVAR: this.data }, timeout: 30000, maxBuffer: 16384, encoding: 'utf8',
            }, (error, stdout, stderr) => {
                if (error) {
                    let code = 'shared_folder_unavailable', message = stderr.trim() || 'Selected folder is unavailable';
                    try {
                        const failure = JSON.parse(stdout).error;
                        if (['shared_folder_permission', 'shared_folder_identity', 'shared_folder_not_configured', 'shared_folder_unavailable'].includes(failure?.code) && typeof failure.message === 'string') {
                            code = failure.code;
                            message = failure.message;
                        }
                    }
                    catch { /* Missing or malformed controller replies stay unavailable. */ }
                    reject(new AppError(code, message));
                    return;
                }
                try {
                    const path = JSON.parse(stdout).path;
                    if (typeof path !== 'string' || !isAbsolute(path))
                        throw new Error('Invalid SDK folder response');
                    resolve(path);
                }
                catch {
                    reject(new AppError('shared_folder_unavailable', 'Invalid SDK folder response'));
                }
            });
        });
    }
}
