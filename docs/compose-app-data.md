# Compose App data

The planner and `compose.lock.json` contain an `app_data` inventory with the
project name and each current project-owned named volume's key and exact name:

```json
{"app_data":{"project":"exampleapp","volumes":[{"key":"db","name":"exampleapp_db"}]}}
```

All project-owned named volumes are App data by default, including volumes
introduced for image-declared storage. External volumes and unsupported custom
names/drivers remain rejected by the planner. The inventory is deterministic;
an empty project has an empty `volumes` list. It describes the current plan,
not runtime existence, contents, or volumes retired by earlier App versions.

## Uninstall behavior

A Compose App does not acquire an executor or root permissions for data cleanup.
Its lifecycle scripts run as the package account. The SDK does not access the
Docker socket or perform privileged named-volume deletion.

The uninstall wizard states before confirmation that container data volumes
are kept, including when the delete-data checkbox is selected. It lists the
exact names from the planner inventory. On exact
`SYNOPKG_PKG_STATUS=UNINSTALL` and an affirmative delete-data choice, the package
account clears the contents it can clear under this package's data, config and
home paths. It validates resolved `/volumeN/@appdata|@appconf|@apphome/<package>`
paths first, leaves DSM's directory roots in place and reports remnants.

The checkbox is `pkgwizard_remove_additional`; `wizard_pkgwizard_remove_additional`,
`delete_app_data` and `wizard_delete_app_data` are accepted aliases. Affirmative
values are `true`, `True`, `TRUE`, `yes` and `1`. Missing or negative values keep
data. Upgrade, stop and unknown lifecycle status never trigger this cleanup.

Named volumes are not deleted. When the current inventory is nonempty and the
user selects delete-data on uninstall, generated postuninst appends an
informational message to `SYNOPKG_TEMP_LOGFILE` in the administrator's DSM
language (nine locales, English fallback), preserving existing text. The message
states that the listed volumes were NOT deleted and ends with
`COMPOSE_DATA_RETAINED`. It does not query Docker: the list comes from the
planner/lock, so it is not a runtime existence check. Older retired volumes may
also remain. No successful full-data-deletion claim is made.

There are no deletion steps in the wizard or message. Container Manager 24.x
has no verified standalone named-volume deletion UI for this workflow; project
Clean/Delete documentation does not establish how to remove orphan volumes
once the package/project is gone. Administrator cleanup, executor-tier Apps
and storage redesign are outside this contract.

## Native sources and verification boundary

- [Script environment variables](https://help.synology.com/developer-guide/synology_package/script_env_var.html)
  distinguishes UNINSTALL from UPGRADE. DSM executes uninstall hooks during upgrades.
- [Wizard UI files](https://help.synology.com/developer-guide/synology_package/wizard/WIZARD_UIFILES_v2.html)
  describes passing component keys and values to scripts. The checkbox key above
  is the generated package's choice, not a universal Docker volume-delete flag.
- [Docker project worker](https://help.synology.com/developer-guide/resource_acquisition/docker-project.html)
  documents project acquire/release, with no public volume-removal option.
- [Resource timing](https://help.synology.com/developer-guide/resource_acquisition/timing.html)
  documents script/worker ordering.
- [Container Manager Project](https://kb.synology.com/tr-tr/DSM/help/ContainerManager/docker_project?version=7)
  describes project Clean/Delete, not an independent orphan-volume UI.

Confidence is high for the documented script/worker contract. Device acceptance
of the final native uninstall message remains required; generated-script tests
and SPK inspection do not prove DSM displays it after uninstall.
