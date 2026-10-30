# Shared-folder lifecycle

The manifest declares one dedicated shared folder with the app-specific name and
exact package-account RO/RW requirement. DSM data-share owns creation and grants;
package hooks and runtime never run as root or write ACLs.

The generated installer prefills the dedicated name. Its optional existing-folder
field performs bounded DSM-origin read requests and refuses unknown accounts,
unknown permission results, unsupported folder kinds and insufficient access.
The default folder can be created before the package account exists; an arbitrary
existing folder is accepted only when package access is already verifiable.

Dedicated-mode upgrade wizards read only the package's generated selection record.
They retain the recorded name, validate it at Next, and permit an explicit switch
to the dedicated folder without moving data. Invalid records and static fallbacks
require a valid explicit selection. Unsubmitted command-line upgrade values retain
the recorded choice.

Compose preparation plans a new dedicated folder on DSM's selected package volume.
DSM acquires the data-share resource before the installed start check. Every start
checks access again with the kernel as the matching package account. A failed
permission check emits a stable status and localized folder/account/RO/RW recovery
message to the DSM hook log. Stopped CGI entries return `app_not_running` and
Package Center Run or Repair instructions without misdiagnosing browser sign-in.

Wizard metadata is advisory. The runtime kernel check is authoritative, child ACLs
can differ and permission revocation does not recall existing descriptors. Unit
and generated-SPK tests do not replace DSM device acceptance of wizard callbacks,
resource timing and status presentation.

See [the API and setup contract](shared-folder.md) and the official
[DSM Data Share resource](https://help.synology.com/developer-guide/resource_acquisition/data_share.html).
