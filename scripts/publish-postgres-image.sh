#!/usr/bin/env bash
#
# publish-postgres-image.sh — Build and publish the ephemeral PostgreSQL image
# (with pg_partman) to the GitHub Container Registry (GHCR).
#
# Counterpart of publish-plugin.sh / publish-validator.sh, but for the Docker
# image. Areas whose changelogs need extra extensions (e.g. bus-central needs
# pg_partman) bake this image in as their no-flag default:
#
#   make build POSTGRES_IMAGE=ghcr.io/<owner>/dbflow-postgres-partman:17.7
#
# The image is BUILT here from docker/postgres-partman/Dockerfile (reproducible),
# then pushed. Nothing needs to exist locally beforehand.
#
# Requirements:
#   - docker running.
#   - A PAT with write:packages scope in $GITHUB_PAT (or $GH_TOKEN).
#
# Usage:
#   GITHUB_PAT=ghp_xxx ./scripts/publish-postgres-image.sh
#   GITHUB_PAT=ghp_xxx IMAGE_TAG=17.7 ./scripts/publish-postgres-image.sh
#   GITHUB_PAT=ghp_xxx OWNER=my-org ./scripts/publish-postgres-image.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# --- Coordinates (override any of these via env) ---------------------------
OWNER="${OWNER:-juanpa-reyest}"                       # GHCR namespace (owner/org)
IMAGE_NAME="${IMAGE_NAME:-dbflow-postgres-partman}"   # image repository name
IMAGE_TAG="${IMAGE_TAG:-17.7}"                        # tag (matches the Postgres version)
DOCKERFILE_DIR="${DOCKERFILE_DIR:-docker/postgres-partman}"

# GHCR requires the namespace and image name to be lowercase.
OWNER="$(printf '%s' "${OWNER}" | tr '[:upper:]' '[:lower:]')"
IMAGE_NAME="$(printf '%s' "${IMAGE_NAME}" | tr '[:upper:]' '[:lower:]')"
IMAGE_REF="ghcr.io/${OWNER}/${IMAGE_NAME}:${IMAGE_TAG}"

# --- Preconditions ----------------------------------------------------------
TOKEN="${GITHUB_PAT:-${GH_TOKEN:-}}"
if [[ -z "${TOKEN}" ]]; then
  echo "error: no token found. Set GITHUB_PAT (or GH_TOKEN) to a PAT with write:packages scope." >&2
  exit 1
fi

command -v docker >/dev/null 2>&1 || { echo "error: docker not on PATH." >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "error: the Docker daemon is not reachable. Start Docker and retry." >&2; exit 1; }

if [[ ! -f "${DOCKERFILE_DIR}/Dockerfile" ]]; then
  echo "error: Dockerfile not found at ${DOCKERFILE_DIR}/Dockerfile" >&2
  exit 1
fi

# --- Log in to GHCR; log out on ANY exit so the token never lingers ---------
logout() { docker logout ghcr.io >/dev/null 2>&1 || true; }
trap logout EXIT

echo "Logging in to ghcr.io as ${OWNER} ..."
printf '%s' "${TOKEN}" | docker login ghcr.io -u "${OWNER}" --password-stdin

# --- Build & push -----------------------------------------------------------
echo "Building ${IMAGE_REF} from ${DOCKERFILE_DIR}/Dockerfile ..."
docker build -t "${IMAGE_REF}" "${DOCKERFILE_DIR}"

echo "Pushing ${IMAGE_REF} ..."
docker push "${IMAGE_REF}"

echo ""
echo "Done. Image published: ${IMAGE_REF}"
echo ""
echo "Next steps:"
echo "  1. Make the package PUBLIC so machines can pull it without authenticating:"
echo "       GitHub -> your profile -> Packages -> ${IMAGE_NAME} -> Package settings -> Change visibility -> Public"
echo "     (A private image would force every developer to 'docker login ghcr.io' first.)"
echo "  2. Bake it as the no-flag default for that area's binary:"
echo "       make build POSTGRES_IMAGE=${IMAGE_REF}"
