#!/bin/bash
set -e

# Create the group if it doesn't exist
getent group "__PKG_GROUP__" >/dev/null || groupadd -r "__PKG_GROUP__"

# Create the user if it doesn't exist (no home; system UID range).
# If FreeIPA/SSS already provides this name, getent succeeds and we reuse that UID.
getent passwd "__PKG_USER__" >/dev/null ||
  useradd -r -M -g "__PKG_GROUP__" -s /sbin/nologin \
    -c "Design Cloud Engine" "__PKG_USER__"

exit 0
