"""A small jubilant-style wrapper around the Terraform CLI.

Wraps a single Terraform root module the way ``jubilant.Juju`` wraps the juju
CLI: thin, typed methods over subprocess calls with logged argv, a ``cli()``
escape hatch, and no hidden state. Designed for Terraform modules that use
the Juju provider — the provider reads the local Juju CLI credentials, so a
``Terraform`` instance composes naturally with a ``jubilant.Juju`` bound to
the model the module manages:

    tf = Terraform(REPO_ROOT / "terraform")
    tf.write_vars({"model_name": "my-model", ...})
    tf.init()
    tf.apply()
    juju = jubilant.Juju(model="my-model")
    juju.wait(jubilant.all_active)
"""

from __future__ import annotations

import json
import pathlib
import shlex
import subprocess
from collections.abc import Mapping
from typing import Any

__all__ = ["Terraform", "TerraformError", "format_tfvars"]


class TerraformError(subprocess.CalledProcessError):
    """Raised when a terraform command exits non-zero.

    Subclasses ``CalledProcessError`` (like jubilant's ``CLIError``) and adds
    captured stderr to the string representation.
    """

    def __str__(self) -> str:
        s = super().__str__()
        if self.stderr:
            s += f"\nstderr:\n{self.stderr}"
        return s


def _format_value(value: Any, indent: str) -> str:
    """Render one Python value as an HCL value."""
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value)
    if isinstance(value, Mapping):
        inner = indent + "  "
        lines = [
            f"{inner}{json.dumps(str(key))} = {_format_value(item, inner)}"
            for key, item in value.items()
        ]
        return "{\n" + "\n".join(lines) + f"\n{indent}}}"
    if isinstance(value, (list, tuple)):
        items = ", ".join(_format_value(item, indent) for item in value)
        return f"[{items}]"
    raise TypeError(f"unsupported tfvars value type: {type(value).__name__}")


def format_tfvars(variables: Mapping[str, Any]) -> str:
    """Render a mapping as ``terraform.tfvars`` content.

    Supports strings, booleans, numbers, lists, and (nested) mappings.
    """
    lines = [
        f"{name} = {_format_value(value, '')}" for name, value in variables.items()
    ]
    return "\n".join(lines) + "\n"


class Terraform:
    """Manage one Terraform root module.

    Args:
        module_dir: Directory containing the root module.
        tfvars_file: Variables file name managed by :meth:`write_vars`,
            relative to *module_dir*.
    """

    def __init__(
        self,
        module_dir: str | pathlib.Path,
        *,
        tfvars_file: str = "terraform.tfvars",
    ) -> None:
        self.module_dir = pathlib.Path(module_dir)
        self.tfvars_path = self.module_dir / tfvars_file

    def __repr__(self) -> str:
        return f"Terraform(module_dir={str(self.module_dir)!r})"

    def cli(self, *args: str, check: bool = True, capture: bool = False) -> str:
        """Run ``terraform <args>`` in the module directory and return stdout.

        When *capture* is false the command streams to the test log (stdout
        is not returned). Raises :class:`TerraformError` on failure when
        *check* is true.
        """
        command = ["terraform", *args]
        print(
            "+",
            "cd",
            str(self.module_dir),
            "&&",
            " ".join(shlex.quote(a) for a in command),
        )
        process = subprocess.run(
            command,
            cwd=self.module_dir,
            check=False,
            text=True,
            capture_output=capture,
        )
        if check and process.returncode != 0:
            raise TerraformError(
                process.returncode, command, process.stdout, process.stderr
            )
        return process.stdout or ""

    def write_vars(self, variables: Mapping[str, Any]) -> pathlib.Path:
        """Write *variables* to the managed tfvars file."""
        self.tfvars_path.write_text(format_tfvars(variables))
        return self.tfvars_path

    def init(self, *, upgrade: bool = False) -> None:
        args = ["init", "-input=false"]
        if upgrade:
            args.append("-upgrade")
        self.cli(*args)

    def validate(self) -> None:
        self.cli("validate")

    def apply(self) -> None:
        self.cli("apply", "-auto-approve", "-input=false")

    def destroy(self, *, check: bool = True) -> None:
        self.cli("destroy", "-auto-approve", "-input=false", check=check)

    def output(self, name: str | None = None) -> Any:
        """Return parsed ``terraform output -json`` values.

        With *name*, returns that output's value; otherwise a dict of
        ``{output_name: value}``.
        """
        raw = self.cli("output", "-json", capture=True)
        outputs = json.loads(raw or "{}")
        if name is not None:
            return outputs[name]["value"]
        return {key: entry["value"] for key, entry in outputs.items()}

    def clean(self) -> None:
        """Best-effort teardown: destroy if vars exist, then remove state.

        Removes the managed tfvars file and the local ``.terraform``
        directory so a subsequent run starts fresh.
        """
        if self.tfvars_path.is_file():
            self.destroy(check=False)
        self.tfvars_path.unlink(missing_ok=True)
        terraform_dir = self.module_dir / ".terraform"
        if terraform_dir.is_dir():
            subprocess.run(["rm", "-rf", str(terraform_dir)], check=False)
