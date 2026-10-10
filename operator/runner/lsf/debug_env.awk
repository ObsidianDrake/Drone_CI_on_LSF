# Executed on the LSF node. ENVIRON preserves multiline values without parsing
# line-oriented `env` output. Uses awk features available on RHEL 6/7/8.
function shell_quote(value,    out,i,c) {
    out = "'"
    for (i = 1; i <= length(value); i++) {
        c = substr(value, i, 1)
        if (c == "'") out = out "'\\''"
        else if (csh && c == "\\") out = out "'\\\\'"
        else if (csh && c == "!") out = out "\\!"
        else if (csh && c == "\n") out = out "\\\n"
        else out = out c
    }
    return out "'"
}
function has_secret(value,    name) {
    for (name in secrets)
        if (ENVIRON[name] != "" && index(value, ENVIRON[name])) return 1
    return 0
}
function comment(label,value) {
    if (has_secret(value)) value = "[omitted: contains a managed secret]"
    gsub(/\n/, "\n# ", value)
    gsub(/\r/, "", value)
    print "# " label value
}
BEGIN {
    csh = (ENVIRON["__DRONE_LSF_SNAPSHOT_SHELL"] ~ /(^|\/)(t?csh)$/)
    n = split(ENVIRON["__DRONE_LSF_SNAPSHOT_SECRETS"], names, " ")
    for (i = 1; i <= n; i++) if (names[i] != "") secrets[names[i]] = 1
    print "# Drone step environment before commands; source with the matching shell."
    comment("Shell: ", ENVIRON["__DRONE_LSF_SNAPSHOT_SHELL"])
    comment("Execution host: ", ENVIRON["__DRONE_LSF_SNAPSHOT_HOST"])
    while ((getline line < "/etc/redhat-release") > 0) comment("OS: ", line)
    close("/etc/redhat-release")
    for (name in ENVIRON) {
        if (name !~ /^[A-Za-z_][A-Za-z0-9_]*$/) continue
        if (name ~ /^__DRONE_LSF_/) continue
        if (name ~ /^LSB_/ || name == "HOST" || name == "HOSTNAME") {
            comment(name " (reference only): ", ENVIRON[name])
            continue
        }
        if (name in secrets || name ~ /^DRONE_LSF_CLONE_/ ||
            name == "GIT_ASKPASS" || name == "SSH_ASKPASS" ||
            name == "_" || name == "SHLVL" || name == "PWD" || name == "OLDPWD" ||
            has_secret(ENVIRON[name])) {
            print "# Omitted variable: " name
            continue
        }
        if (csh) print "setenv " name " " shell_quote(ENVIRON[name])
        else print "export " name "=" shell_quote(ENVIRON[name])
    }
    cwd = ENVIRON["__DRONE_LSF_SNAPSHOT_CWD"]
    if (has_secret(cwd)) print "# Working directory omitted: contains a managed secret."
    else print "cd " shell_quote(cwd)
}
