"""Extract the Azure CLI command catalog for zshai (PLAN.md §3.4).

Run with az's own Python so the installed modules and extensions are what get catalogued:

    /opt/az/bin/python3 -I catalog/az_extract.py catalog/az.json          # Linux .deb (or: zshai az-catalog)
    $(brew --prefix azure-cli)/libexec/bin/python -I catalog/az_extract.py catalog/az.json  # macOS

Output: {"version", "groups": {group: summary}, "commands": {"vm create": {...}}}
Each command has its options (all spellings), which are required, choices (short lists only),
whether it supports --yes / --no-wait / what-if, and a risk class derived from its verb.
"""
import json
import sys
import warnings
import logging

warnings.filterwarnings("ignore")
logging.disable(logging.CRITICAL)

from azure.cli.core import get_default_cli, __version__ as core_version  # noqa: E402

READ_VERBS = {"show", "list", "get", "exists", "check", "query", "download", "tail", "wait", "browse",
              "list-locations", "list-skus", "list-sizes", "show-*", "list-*", "get-*", "check-*", "test"}
DELETE_VERBS = {"delete", "purge", "remove", "delete-batch", "purge-deleted", "deallocate", "clear", "reset",
                "regenerate-key", "renew", "revoke", "unlink", "detach", "disable", "stop", "restart"}


def verb_risk(name: str) -> str:
    verb = name.split()[-1]
    if verb in READ_VERBS or verb.startswith(("show", "list", "get-", "check")):
        return "read"
    if verb in DELETE_VERBS or verb.startswith(("delete", "purge", "remove")):
        return "del"
    return "change"


def main(out_path: str) -> None:
    cli = get_default_cli()
    cli.invocation = cli.invocation_cls(cli_ctx=cli, parser_cls=cli.parser_cls,
                                        commands_loader_cls=cli.commands_loader_cls, help_cls=cli.help_cls)
    loader = cli.invocation.commands_loader
    table = loader.load_command_table(None)
    groups = {}
    for gname, g in sorted(loader.command_group_table.items()):
        if not gname:
            continue
        summary = ""
        try:
            summary = (g.group_kwargs.get("help") or "") if hasattr(g, "group_kwargs") else ""
        except Exception:
            pass
        groups[gname] = summary.split("\n")[0][:160]

    # Load every argument in one pass: az applies its argument registry (option names,
    # required flags, ignored internals) only when called with command=None.
    loader.load_arguments()
    try:
        import yaml
        from knack.help_files import helps
    except Exception:  # pragma: no cover
        helps, yaml = {}, None

    commands = {}
    failed = 0
    for name in sorted(loader.command_table):
        cmd = loader.command_table[name]
        params = []
        for arg_name, arg in sorted((getattr(cmd, "arguments", None) or {}).items()):
            settings = arg.type.settings
            opts = [o for o in (settings.get("options_list") or []) if isinstance(o, str)]
            if not opts:
                continue  # positional/internal (cmd, etc.)
            if settings.get("help") == "==SUPPRESS==" or arg_name == "cmd":
                continue
            p = {"o": opts}
            if settings.get("required"):
                p["req"] = True
            choices = settings.get("choices")
            if choices and len(list(choices)) <= 12:
                p["ch"] = [str(c) for c in choices]
            if settings.get("action") in ("store_true", "store_false") or settings.get("nargs") == 0:
                p["flag"] = True
            params.append(p)
        all_opts = {o for p in params for o in p["o"]}
        desc = ""
        try:
            d = cmd.description
            desc = (d() if callable(d) else d) or ""
        except Exception:
            pass
        entry = {
            "risk": verb_risk(name),
            "p": params,
            "h": desc.split("\n")[0][:160],
        }
        if "--yes" in all_opts or "-y" in all_opts:
            entry["yes"] = True
        if "--no-wait" in all_opts:
            entry["nowait"] = True
        if getattr(cmd, "deprecate_info", None):
            entry["deprecated"] = True
        if getattr(cmd, "preview_info", None) or getattr(cmd, "experimental_info", None):
            entry["preview"] = True
        if yaml is not None and name in helps:
            try:
                doc = yaml.safe_load(helps[name]) or {}
                ex = [e.get("text", "").strip() for e in doc.get("examples", []) if isinstance(e, dict)]
                ex = [e for e in ex if e.startswith("az ")][:5]
                if ex:
                    entry["ex"] = ex
            except Exception:
                pass
        commands[name] = entry

    # what-if support lives in separate commands (deployment group what-if, ...)
    for name in commands:
        if name.endswith(" create") and (name.rsplit(" ", 1)[0] + " what-if") in commands:
            commands[name]["whatif"] = name.rsplit(" ", 1)[0] + " what-if"

    out = {"version": core_version, "groups": groups, "commands": commands}
    with open(out_path, "w") as f:
        json.dump(out, f, separators=(",", ":"), sort_keys=True)
    print(f"az {core_version}: {len(groups)} groups, {len(commands)} commands, "
          f"{sum(len(c['p']) for c in commands.values())} options, {failed} failed to load", file=sys.stderr)


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else "az.json")
