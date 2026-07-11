#!/bin/sh
set -e

# Bind-mounted /data is often owned by root on the host; fix before dropping privileges.
if [ "$(id -u)" = "0" ] && [ -d /data ]; then
	mkdir -p /data
	chown 10001:10001 /data
	exec runuser -u video-dl-bot -- /bin/video-dl-bot "$@"
fi

exec /bin/video-dl-bot "$@"
