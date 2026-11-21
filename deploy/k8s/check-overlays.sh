#!/usr/bin/env bash
# Builds every kustomization that a person applies, and checks what it
# builds against the schemas of Kubernetes, without a cluster. Go's
# tests read the kustomizations as YAML; only kustomize itself notices a
# replacement that finds no target, a patch of an object that is not
# there, or a field that Kubernetes does not have.
#
#   deploy/k8s/check-overlays.sh [--require-kubeconform]
#   deploy/k8s/check-overlays.sh --list
#
# The kustomizations are found by the rule of TestEveryOverlayHasItsValues
# (cloud_test.go): capture/, overlays/*/ and overlays/*/capture/. Then the
# variants that a comment in a kustomization tells a person to take in,
# which the script builds from a kustomization of its own, so that no
# file of the repository changes. --list prints them all and builds
# nothing; TestCheckOverlaysListsEveryKustomization compares the list
# with the Go rule.
#
# The cloud environments read their values from values.local.env, which
# Git ignores (environments/aws, environments/azure). The script builds
# a copy of deploy/k8s in which each of these files is its committed
# example, values.local.env.example, so that the check needs no file of
# your own and gives the same result everywhere; your own file is not
# used. The example's addresses build all the same: the
# init container of the P-CSCF refuses them at run time.
#
# kubeconform checks the output with -strict (no field that the schema
# does not have) against KUBERNETES_VERSION. It fetches the schemas from
# raw.githubusercontent.com (the repository yannh/kubernetes-json-schema)
# unless KUBECONFORM_SCHEMA_LOCATION names another place. Without
# kubeconform the script only builds, and says so; --require-kubeconform
# makes that a failure, for CI.
#
# Environment:
#   KUBECTL                      the command (default kubectl)
#   KUBECONFORM                  the command (default kubeconform)
#   KUBERNETES_VERSION           the schemas to check against (default
#                                1.37.2, the kubectl of ci/)
#   KUBECONFORM_SCHEMA_LOCATION  passed to kubeconform -schema-location
set -euo pipefail
cd "$(dirname "$0")"
export MSYS_NO_PATHCONV=1

usage() {
	echo "usage: deploy/k8s/check-overlays.sh [--require-kubeconform | --list]" >&2
	exit 2
}
require_kubeconform=0
list=0
for arg in "$@"; do
	case "$arg" in
	--require-kubeconform) require_kubeconform=1 ;;
	--list) list=1 ;;
	*) usage ;;
	esac
done

# The variants: a name, the kustomization they start from, and the
# components they add. overlays/cloud-azure names the two load balancer
# components in a comment, the first alone for load_balancer = "sip" in
# deploy/cloud/azure, both for "sip-and-rtp".
variants=(
	"overlays/cloud-azure+azure-load-balancer-sip|overlays/cloud-azure|components/azure-load-balancer-sip"
	"overlays/cloud-azure+azure-load-balancer-sip+azure-load-balancer-rtp|overlays/cloud-azure|components/azure-load-balancer-sip components/azure-load-balancer-rtp"
)

applied=()
for dir in capture overlays/*/ overlays/*/capture/; do
	dir="${dir%/}"
	[ -f "$dir/kustomization.yaml" ] && applied+=("$dir")
done

if [ "$list" = 1 ]; then
	printf '%s\n' "${applied[@]}"
	for variant in "${variants[@]}"; do
		echo "${variant%%|*}"
	done
	exit 0
fi

KUBECTL="${KUBECTL:-kubectl}"
KUBECONFORM="${KUBECONFORM:-kubeconform}"
KUBERNETES_VERSION="${KUBERNETES_VERSION:-1.37.2}"
command -v "$KUBECTL" >/dev/null || {
	echo "FAIL $KUBECTL is not installed (README.md, Before you start)" >&2
	exit 2
}
conform=1
if ! command -v "$KUBECONFORM" >/dev/null; then
	if [ "$require_kubeconform" = 1 ]; then
		echo "FAIL $KUBECONFORM is not installed, and --require-kubeconform was given" >&2
		exit 2
	fi
	conform=0
	echo "NOTE $KUBECONFORM is not installed: the overlays are built, not checked against the schemas" >&2
fi
conform_args=(-strict -summary -kubernetes-version "$KUBERNETES_VERSION")
if [ -n "${KUBECONFORM_SCHEMA_LOCATION:-}" ]; then
	conform_args+=(-schema-location "$KUBECONFORM_SCHEMA_LOCATION")
fi

# kustomize takes a directory only by a relative path, so the copy and
# the variants' kustomizations live in a directory next to the others,
# removed at the end.
work="$(mktemp -d .check-overlays.XXXXXX)"
trap 'rm -rf "$work"' EXIT
tree="$work/tree"
mkdir "$tree"
# */ leaves out the work directories, whose names start with a dot.
for dir in */; do
	cp -R "${dir%/}" "$tree/"
done
for example in "$tree"/environments/*/values.local.env.example; do
	cp "$example" "${example%.example}"
done

failed=()
check() {
	local name="$1" dir="$2"
	# kubeconform names the file in its messages.
	local out="$work/${name//[\/+]/_}.yaml"
	echo "== $name"
	if ! "$KUBECTL" kustomize "$tree/$dir" >"$out"; then
		echo "FAIL $name: kubectl kustomize $dir" >&2
		failed+=("$name")
		return
	fi
	if [ "$conform" = 1 ] && ! "$KUBECONFORM" "${conform_args[@]}" "$out"; then
		echo "FAIL $name: kubeconform" >&2
		failed+=("$name")
		return
	fi
	echo "ok   $name ($(grep -c '^kind: ' "$out") objects)"
}

for dir in "${applied[@]}"; do
	check "$dir" "$dir"
done
n=0
for variant in "${variants[@]}"; do
	IFS='|' read -r name base components <<<"$variant"
	n=$((n + 1))
	dir="variants/$n"
	mkdir -p "$tree/$dir"
	{
		echo "apiVersion: kustomize.config.k8s.io/v1beta1"
		echo "kind: Kustomization"
		echo "resources:"
		echo "  - ../../$base"
		echo "components:"
		for component in $components; do
			echo "  - ../../$component"
		done
	} >"$tree/$dir/kustomization.yaml"
	check "$name" "$dir"
done

if [ "${#failed[@]}" -gt 0 ]; then
	echo "FAIL ${#failed[@]} of the kustomizations: ${failed[*]}" >&2
	exit 1
fi
echo "PASS every kustomization builds$([ "$conform" = 1 ] && echo " and matches the schemas of Kubernetes $KUBERNETES_VERSION")"
