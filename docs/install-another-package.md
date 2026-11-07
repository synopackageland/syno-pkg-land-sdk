# Offering another Package Land package

An App that works better with another Package Land package (Connect, for
example) can offer it. The App never installs it: DSM's Package Center does,
and only a DSM administrator can install a package.

## Where packages come from

Package Land packages install from the package source
`https://packages.synopkgland.com/`. An administrator adds it once in Package
Center (Settings > Package Sources); its packages then show in Package Center
like any other.

## The handoff

1. **Detect.** The App's backend checks whether the other package is
   installed, the way the identity gateway checks for Connect: its
   `/var/packages/<Package>/INFO` exists. The App's frontend asks its backend
   through one of its own methods; a page cannot see installed packages.
2. **Open the other App.** If it is installed, link to it with
   `dsmLaunchURL(origin, packageId, params)`, which can also pass a route.
   If it is not, an administrator (`ctx.user.isAdmin`) gets an **Open Package
   Center** link from `packageCenterUrl()`, with a line saying which package
   to install and that the package source above must be added. Other users
   are asked to contact their administrator.
3. **The user acts** in that window: installs the package, or sets something
   up in the other App.
4. **Re-check.** The App detects again when its window regains focus or the
   user presses **Try again**, and then drops the offer. It never assumes the
   user finished.

```ts
import {dsmLaunchURL, packageCenterUrl} from '@synopackageland/sdk'

const installed = await ctx.call<boolean>('connect.installed')
const href = installed
  ? dsmLaunchURL(location.origin, 'SynoPkgConnect', {route: '/apps/MyApp'})
  : ctx.user?.isAdmin ? packageCenterUrl() : undefined
```

`packageCenterUrl(origin?)` returns `/?launchApp=SYNO.SDS.PkgManApp.Instance`,
or that link on `origin`. It opens Package Center only. DSM documents no link
to one package's page or its install button, so the App names the package in
its own text.
