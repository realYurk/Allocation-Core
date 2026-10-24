#!/bin/sh
cd "$(dirname "$0")" || exit 1
./run.sh smoke
printf '\nPress Enter to close...\n'
read answer
