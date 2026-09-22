#!/bin/bash

# The release workflow runs this script. The build steps are in the Makefile:
# see "make release".

set -e

cd "$(dirname "$0")"
exec make release
