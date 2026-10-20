#!/bin/sh
# synopkgland executor control
set -eu
umask 77
export PATH=/usr/bin:/bin
p=@PACKAGE@
q=/var/packages/$p
s=$q/target/executor
r=/usr/local/etc/synopkgland/$p
d=$r/runtime
refuse(){ rm -f "$d/.executor" "$d/.policy.json";logger -t synopkgland "$p: $1";exit 1;}
v(){ echo "$3@POLICY_DIGEST@  $1/$2policy.json"|sha256sum -c - >/dev/null || refuse digest;}
mkdir -p -m 700 "$d"
[ "$(find "$d" -maxdepth 0 -uid 0 -type d -perm 700)" = "$d" ]
a=$(cat "$q/var/executor/control")
case "$a" in start|stop|purge) ;; *) exit 1;; esac
if [ "$a" = stop ]; then
v "$d" '' ''
exec "$d/executor" control stop --package "$p"
fi
# Root runs only executor bytes anchored in the root-owned INFO, under the approved policy.
e=$(sed -n 's/^synopkgland_executor_sha256="\([0-9a-f]*\)"$/\1/p' "$q/INFO") || refuse anchor
[ ${#e} = 64 ] || refuse anchor
cp "$s/executor" "$d/.executor" || refuse digest
cp "$s/policy.json" "$d/.policy.json" || refuse digest
v "$d" . "$e  $d/.executor
"
chmod 700 "$d/.executor"
[ "$a" = start ] || exec "$d/.executor" control purge --package "$p"
for f in executor policy.json; do mv "$d/.$f" "$d/$f"; done
"$d/executor" install-root --package "$p"
"$d/executor" control start --package "$p"
