# Sourced by the scripts that run Docker. The engine may be on another host:
# SYNOPKGLAND_DOCKER_SSH=<ssh host>.

# engine <command>: runs command with sh on the Docker host.
engine() {
	if test -n "${SYNOPKGLAND_DOCKER_SSH:-}"; then
		# The remote login shell may not be sh: it only starts sh.
		ssh "$SYNOPKGLAND_DOCKER_SSH" "sh -c $(shell_quote "$1")"
	else
		sh -c "$1"
	fi
}

shell_quote() {
	printf "'%s'" "$(printf %s "$1" | sed "s/'/'\\\\''/g")"
}

# Shared on the Docker host, by every synopkgland repository's gate:
# synopkgland-gate-<repo>:<tag> images and synopkgland-cache-* volumes. They
# stay between runs; a run removes only its own containers.

# gate_image_name <repo> <Dockerfile>: the tag is the first 12 hex digits of
# the Dockerfile's SHA-256, so a changed Dockerfile is a new image.
gate_image_name() {
	printf 'synopkgland-gate-%s:%s\n' "$1" "$(digest_file "$2" | cut -c1-12)"
}

digest_file() {
	if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1
	else shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# gate_image <repo> <Dockerfile>: builds the image unless the host has it,
# removes this repository's images with other tags, and prints its name.
gate_image() {
	local image repository
	image=$(gate_image_name "$1" "$2")
	repository=${image%%:*}
	engine "docker image inspect $image >/dev/null 2>&1 || docker build -q -t $image - >/dev/null" <"$2"
	# An image a running gate still uses stays: docker rmi refuses it.
	engine "docker images --format '{{.Repository}}:{{.Tag}}' $repository | grep -vx '$image' \
		| xargs -r docker rmi >/dev/null 2>&1 || true"
	echo "$image"
}

# cache_volumes <image> <volume>...: creates the volumes the host lacks and
# gives them to uid 1000, the gates' user.
cache_volumes() {
	local image=$1 mounts= volume
	shift
	for volume in "$@"; do mounts="$mounts -v $volume:/cache/$volume"; done
	engine "docker run --rm --user 0:0 $mounts $image chown 1000:1000 $(printf '/cache/%s ' "$@")"
}

# slot_command <command>: command, run once one of the host's two gate slots
# is free. Only two heavy gate runs share the host at a time.
slot_command() {
	local command
	command="sh -c $(shell_quote "$1")"
	printf '%s' "said=; while :; do for slot in 0 1; do \
flock -n -E 75 /tmp/synopkgland-gate.slot\$slot.lock $command; status=\$?; test \$status = 75 || exit \$status; done; \
test -n \"\$said\" || echo 'gate: both gate slots on the Docker host are busy; waiting' >&2; said=1; sleep 5; done"
}
