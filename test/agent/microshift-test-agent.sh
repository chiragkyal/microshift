#!/bin/bash

set -xeuo pipefail

GREENBOOT_CONFIGURATION_FILE=/etc/greenboot/greenboot.conf
AGENT_CFG=/var/lib/microshift-test-agent.json

# Example config
# {
#     "deploy-id": {
#         "every": [ "prevent_backup" ],
#         "1": [ "fail_greenboot" ],
#         "2": [ "..." ],
#         "3": [ "..." ]
#     }
# }

CLEANUP_CMDS=()
_cleanup() {
    for cmd in "${CLEANUP_CMDS[@]}"; do
        ${cmd}
    done
}
trap "_cleanup" SIGTERM SIGQUIT INT

_run_actions() {
    local -r actions="${1}"
    if [[ "${actions}" == "null" ]]; then
        return
    fi

    num=$(echo "${actions}" | jq -c ". | length")
    for i in $(seq 0 $((num - 1))); do
        action=$(echo "${actions}" | jq -c -r ".[${i}]")

        if ! declare -F "${action}"; then
            echo "Unknown action: ${action}"
        else
            "${action}"
        fi
    done
}

_debug_info() {
    grub2-editenv - list || true
    ostree admin status -v || true
    rpm-ostree status -v || true
    journalctl --list-boots --reverse | head -n6 || true
    ls -lah /var/lib/ || true
    ls -lah /var/lib/microshift || true
    ls -lah /var/lib/microshift-backups || true
    cat "${AGENT_CFG}" || true
}

_get_current_boot_number() {
    if ! /usr/bin/grub2-editenv list | grep -q boot_counter; then
        echo "boot_counter is missing - script only for newly staged deployments"
        exit 0
    fi

    local -r boot_counter=$(/usr/bin/grub2-editenv list | grep boot_counter | sed 's,boot_counter=,,')
    local max_boot_attempts

    if test -f "${GREENBOOT_CONFIGURATION_FILE}"; then
        # shellcheck source=/dev/null
        source "${GREENBOOT_CONFIGURATION_FILE}"
    fi

    if [ -v GREENBOOT_MAX_BOOT_ATTEMPTS ]; then
        max_boot_attempts="${GREENBOOT_MAX_BOOT_ATTEMPTS}"
    else
        max_boot_attempts=3
    fi

    # When deployment is staged, greenboot sets boot_counter to 3
    # and this variable gets decremented on each boot.
    # First boot of new deployment will have it set to 2.
    echo "$((max_boot_attempts - boot_counter))"
}

prevent_backup() {
    local -r path="/var/lib/microshift-backups"
    if [[ ! -e "${path}" ]]; then
        mkdir -vp "${path}"

        # because of immutable attr, if the file does not exist, it can't be created
        touch "${path}/health.json"
    fi
    # prevents from creating a new backup directory, but doesn't prevent from updating health.json
    chattr -V +i "${path}"
    CLEANUP_CMDS+=("chattr -V -i ${path}")
}

fail_greenboot() {
    local -r path="/etc/greenboot/check/required.d/99_microshift_test_failure.sh"
    cat >"${path}" <<EOF
#!/bin/bash
echo 'Forced greenboot failure by MicroShift Failure Agent'
sleep 5
exit 1
EOF
    chmod +x "${path}"
    CLEANUP_CMDS+=("rm -v ${path}")
}

# WORKAROUND START
# When going from "just RHEL" to RHEL+MicroShift+dependencies there might be a
# problem with groups. Following line ensures that sshd's keys are owned by
# ssh_keys group and not some other.
# When removing, change Before= in microshift-test-agent.service
chown -v root:ssh_keys /etc/ssh/ssh_host*key
# WORKAROUND END

_debug_info

current_boot="$(_get_current_boot_number)"
current_deployment_id=$(rpm-ostree status --booted --json | jq -r ".deployments[0].id")

deploy=$(jq -c ".\"${current_deployment_id}\"" "${AGENT_CFG}")
if [[ "${deploy}" == "null" ]]; then
    exit 0
fi

every_boot_actions=$(echo "${deploy}" | jq -c ".\"every\"")
current_boot_actions=$(echo "${deploy}" | jq -c ".\"${current_boot}\"")

_run_actions "${every_boot_actions}"
_run_actions "${current_boot_actions}"

# sleep until the reboot, will run cleanup upon exit
sleep infinity
