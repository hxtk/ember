def _oci2cpio_impl(ctx):
    oci_layout = ctx.attr.oci[DefaultInfo].files.to_list()
    if len(oci_layout) != 1:
        fail(
            "Expected exactly one OCI layout directory, got %d" % len(oci_layout)
        )

    oci_dir = oci_layout[0]
    out = ctx.outputs.out

    ctx.actions.run_shell(
        command = ctx.executable._tool.path + " $1 > " + out.path,
        arguments = [
            oci_dir.path,
        ],
        inputs = [oci_dir],
	tools = [ctx.executable._tool],
        outputs = [out],
        mnemonic = "OCI2CPIO",
        progress_message = "Converting OCI layout to CPIO: %s" % out.short_path,
        use_default_shell_env = False,
    )

    return [
        DefaultInfo(files = depset([out])),
    ]


oci2cpio = rule(
    implementation = _oci2cpio_impl,
    attrs = {
        # rules_oci output (OCI layout directory)
        "oci": attr.label(
            mandatory = True,
            providers = [DefaultInfo],
        ),

        # Output CPIO archive
        "out": attr.output(
            mandatory = True,
        ),

        # Tool binary
        "_tool": attr.label(
            default = Label("//cmd/oci2cpio"),
            executable = True,
            cfg = "exec",
        ),
    },
    doc = "Converts an OCI layout directory into a CPIO archive using //cmd/oci2cpio",
)

