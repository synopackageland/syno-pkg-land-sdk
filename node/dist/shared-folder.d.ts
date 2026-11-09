export interface SharedFolderOptions {
    packageId: string;
    dataDir?: string;
    payloadRoot?: string;
}
/** Installer-selected folder, checked by the SDK controller as this package UID.
 * Configure this once in trusted App code. No DSM session or ACL writer is used.
 */
export declare class SharedFolderConfig {
    private readonly options;
    private readonly root;
    private readonly data;
    constructor(options: SharedFolderOptions);
    /** Re-resolve volume + share + subpath and require the declared RO/RW access. */
    require(key: string): Promise<string>;
}
