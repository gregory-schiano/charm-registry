# Craft Icons and Snap Documentation Design

## Goal

Use the existing project artwork in Craft store listings and improve the
repository documentation for users installing the Snap Store package.

## Artwork

The files under `medias/` remain the source artwork:

- `medias/charm_registry_app_icon_purple_256.svg` is the application icon.
- `medias/charm_registry_lockup_DARK_BG_TRANSPARENT_vector.png` is the project
  logo used in documentation.

Copy the application icon to the conventional locations consumed by the
packaging tools:

- `charm/icon.svg` for Charmhub.
- `snap/snap/gui/icon.svg` for the Snap Store.

The copied files are packaging inputs. The originals remain available for
documentation and future branding work. Rockcraft has no supported store-icon
field, so `rockcraft.yaml` is unchanged.

## Snap Store metadata

Update `snap/snapcraft.yaml` with the packaged icon and project links:

- `icon: snap/gui/icon.svg`
- `website: https://github.com/gregory-schiano/charm-registry`
- `source-code: https://github.com/gregory-schiano/charm-registry`
- `issues: https://github.com/gregory-schiano/charm-registry/issues`

Keep `spellbook` as the snap name. The documentation must explain that this is
the temporary Snap Store package name while the installed service and commands
retain the `charm-registry` naming.

## Documentation

Add the wide project logo near the top of the root `README.md`, using a relative
repository path so it renders on GitHub.

Keep detailed snap instructions in `docs/deployment.md`, which already owns
deployment guidance. Expand its Snap section to cover:

- installing `spellbook`;
- setting the required `oci.secret-key`;
- starting, stopping, restarting, and inspecting the service;
- viewing logs and current snap configuration;
- configuring standalone and production deployments;
- invoking the packaged `charm-registryctl` command;
- links to the project repository and issue tracker.

Correct existing snap management examples from the old package name
`charm-registry` to `spellbook`. Keep application concepts, configuration
environment variables, and service descriptions named Charm Registry.

The root README should retain a concise deployment overview and link readers to
the detailed deployment guide rather than duplicate the full snap instructions.

## Validation

- Parse all modified YAML files.
- Confirm both copied SVG files are byte-identical to the source icon.
- Check that all local Markdown image paths resolve.
- Search snap documentation for stale `snap ... charm-registry` commands.
- Run `git diff --check`.

