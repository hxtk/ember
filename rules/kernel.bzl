load("@rules_cc//cc:find_cc_toolchain.bzl", "find_cc_toolchain")
load("@rules_foreign_cc//toolchains/native_tools:tool_access.bzl", "get_make_data")

KernelConfigInfo = provider(
    doc = "Resolved kernel .config",
    fields = {
        "config": "The final .config file",
    },
)

def _kernel_config_impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name + ".config")

    # 1. Prepare the content to append (initramfs and cmdline)
    extra_content = ""
    if ctx.attr.cmdline:
        # Escape quotes to ensure valid Kconfig syntax
        extra_content += 'CONFIG_CMDLINE_BOOL=y\nCONFIG_CMDLINE="{}"\n'.format(
            ctx.attr.cmdline.replace('"', '\\"')
        )

    if ctx.file.initramfs:
        # Point to the initramfs file path
        extra_content += 'CONFIG_INITRAMFS_SOURCE="{}"\n'.format(ctx.file.initramfs.path)

    # 2. Write the extra content to a temporary file
    # Using ctx.actions.write handles text and quoting safely
    extra_file = ctx.actions.declare_file(ctx.label.name + "_extras.config")
    ctx.actions.write(
        output = extra_file,
        content = extra_content,
    )

    # 3. Concatenate the user's config with the extras
    # We use 'cat' to merge the files. No kernel scripts required.
    ctx.actions.run_shell(
        inputs = [ctx.file.config, extra_file],
        outputs = [out],
        command = "cat {} {} > {}".format(
            ctx.file.config.path,
            extra_file.path,
            out.path
        ),
        progress_message = "Finalizing kernel config {}".format(ctx.label),
    )

    return [KernelConfigInfo(config = out)]

kernel_config = rule(
    implementation = _kernel_config_impl,
    attrs = {
        # Renamed from 'base' to 'config' to reflect it is a full config
        "config": attr.label(
            allow_single_file = True,
            mandatory = True,
            doc = "The fully merged .config file used as a base",
        ),
        "cmdline": attr.string(),
        "initramfs": attr.label(
            allow_single_file = True,
            doc = "CPIO archive to embed as initramfs",
        ),
    },
)

def _kernel_build_impl(ctx):
    cc = find_cc_toolchain(ctx)
    make_data = get_make_data(ctx)

    tools = {
        "CC": cc.compiler_executable,
        "LD": cc.ld_executable,
        "AR": cc.ar_executable,
        "NM": cc.nm_executable,
        "OBJDUMP": cc.objdump_executable,
        "OBJCOPY": cc.objcopy_executable,
        "STRIP": cc.strip_executable,
    }

    out = ctx.outputs.out
    workdir = ctx.actions.declare_directory(ctx.label.name + "_build")
    script = ctx.actions.declare_file(ctx.label.name + ".sh")

    config = ctx.attr.config[KernelConfigInfo].config

    make_env = ["{}=$EXT_BUILD_ROOT/{}".format(k, v) for k, v in tools.items()]
    make_env += ["MAKE="+make_data.path]

    root = ctx.files.srcs[0].dirname

    ctx.actions.write(
        output = script,
        is_executable = True,
        content = """\
set -euo pipefail

export ARCH={arch}
export KCONFIG_NOTIMESTAMP=1
export SOURCE_DATE_EPOCH=1
export EXT_BUILD_ROOT=$PWD
export {make_env}

cp -a {src}/* {workdir}
chmod -R u+w {workdir}
cp {config} {workdir}/.config
cd {workdir}

{make} olddefconfig {make_env}
{make} -j$(nproc) {make_env}

cp arch/{arch}/boot/bzImage {out}
""".format(
            arch = ctx.attr.arch,
            src = root,
            config = config.path,
            workdir = workdir.path,
            out = out.path,
            make_env = " ".join(make_env),
            make = make_data.path,
        ),
    )

    env = {
        "LLVM": "1",
        "LLVM_IAS": "1",
        "ARCH": ctx.attr.arch,
        "KCONFIG_NOTIMESTAMP": "1",
        "SOURCE_DATE_EPOCH": "1",
    }

    make_info = make_data.target[DefaultInfo]
    ctx.actions.run(
        executable = script,
        inputs = depset(
            direct = [config] + ctx.files.srcs,
            transitive = [cc.all_files, make_info.files],
        ),
        outputs = [out, workdir],
        env = env,
        mnemonic = "KernelBuild",
        progress_message = "Building Linux kernel {}".format(ctx.label),
    )


kernel_build = rule(
    implementation = _kernel_build_impl,
    attrs = {
        "srcs": attr.label(
            allow_files = True,
            doc = "Filegroup containing kernel sources",
        ),
        "config": attr.label(
            providers = [KernelConfigInfo],
            doc = "kernel_config target",
        ),
        "arch": attr.string(mandatory = True),
        "out": attr.output(mandatory = True),
    },
    toolchains = [
        "@bazel_tools//tools/cpp:toolchain_type",
        "@rules_foreign_cc//toolchains:make_toolchain",
    ],
)
