#!/bin/sh
# synopkgland executor boot
set -eu
umask 77
export PATH=/usr/bin:/bin
p=@PACKAGE@
q=/var/packages/$p
s=$q/target/executor
r=/usr/local/etc/synopkgland/$p
d=$r/runtime
o="--package $p"
v(){ echo "$2@POLICY_DIGEST@  ${1}policy.json"|sha256sum -c - >/dev/null||{ logger digest;exit 1;};}
a=purge
if [ -d $q ];then
a=stop
[ "$(cat $q/var/executor/control)" != purge ] || { a=purge;[ -e $d ] || [ -L $d ] || { logger purged;exit 0;};}
else [ -d $r ] || exit 0;fi
mkdir -p -m 700 $d
[ "$(find $d -maxdepth 0 -uid 0 -type d -perm 700)" = $d ]
cd $d
[ $a = stop ] || { v ./ "";exec ./executor control purge $o;}
e=$(sed -n 's/^synopkgland_executor_sha256="\([0-9a-f]*\)"$/\1/p' $q/INFO)
[ ${#e} = 64 ]
cp $s/executor .executor
cp $s/policy.json .policy.json
v . "$e  .executor
"
chmod 700 .executor
for f in executor policy.json;do mv .$f $f;done
./executor install-root $o
./executor control start $o
