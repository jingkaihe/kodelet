#!/usr/bin/env bash
# Offline release-download checks; never install into the caller's HOME or PATH.
set -euo pipefail

installer="$(cd "$(dirname "$0")/.." && pwd)/install.sh"
trial=$(mktemp -d)
trap 'rm -rf "$trial"' EXIT
tag=v1.2.3

gh() {
    case "$1" in
        auth) [[ "$2" = status && "$scenario" != anonymous ]] ;;
        api)
            [ "$scenario" = latest ] || return 1
            echo "$tag"
            ;;
        release)
            [ "$2" = download ] && [ "$3" = "$tag" ] || return 1
            [ "$4 $5" = '--repo github.com/jingkaihe/kodelet' ] || return 1
            [ "$6" = --pattern ] && [ "$8" = --output ] || return 1
            if [ "$scenario" = retry ] && [[ "$7" == kodelet-clipboard-* ]]; then
                echo partial > "$9"
                return 1
            fi
            if [ "$scenario" = legacy ] && [[ "$7" == kodelet-extension-schedule-* ]]; then
                return 1
            fi
            echo "gh:$7" > "$9"
            ;;
        *) return 1 ;;
    esac
}

curl() {
    local url="${!#}"
    [ "$scenario" != latest ] || return 1 # Authenticated latest install needs no curl.
    case "$url" in
        https://api.github.com/*) return 22 ;; # Anonymous API rate limit.
        https://github.com/jingkaihe/kodelet/releases/latest)
            echo "https://github.com/jingkaihe/kodelet/releases/tag/$tag"
            ;;
        https://github.com/jingkaihe/kodelet/releases/download/"$tag"/*)
            [ "$4" = -o ] || return 1
            if [ "$scenario" = legacy ] && [[ "${url##*/}" == kodelet-extension-schedule-* ]]; then
                echo "Not Found" > "$5"
                echo 404 # Releases before the schedule extension.
                return 0
            fi
            echo "curl:${url##*/}" > "$5"
            echo 200
            ;;
        *) return 1 ;;
    esac
}

# Keep the installer's optional system symlinks in the temporary directory.
ln() { command ln -sf "$2" "$HOME/${3##*/}"; }

for scenario in latest retry anonymous legacy; do
    home="$trial/$scenario"
    mkdir -p "$home"
    (
        export HOME="$home" KODELET_INSTALL_DIR="$home/prefix" KODELET_VERSION=latest
        if [[ "$scenario" = retry || "$scenario" = legacy ]]; then KODELET_VERSION="${tag#v}"; fi
        source "$installer" --binary
    )
    for binary in kodelet kodelet-extension-mcp kodelet-extension-schedule kodelet-clipboard; do
        path="$home/prefix/bin/$binary"
        if [ "$scenario" = legacy ] && [ "$binary" = kodelet-extension-schedule ]; then
            [ ! -e "$path" ] # A missing optional asset is skipped, not installed.
            continue
        fi
        transport=gh
        if [[ "$scenario" = anonymous || ( "$scenario" = retry && "$binary" = kodelet-clipboard ) ]]; then
            transport=curl
        fi
        [[ -x "$path" && "$(cat "$path")" == "$transport:$binary-"* ]]
    done
done
