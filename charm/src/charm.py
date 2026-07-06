#!/usr/bin/env python3
# Copyright 2026 gschiano
# See LICENSE file for licensing details.

"""Go Charm entrypoint."""

import logging
import typing

import ops
import paas_charm.go
from charms.traefik_k8s.v2.ingress import IngressPerAppRequirer
from paas_charm.app import App
from paas_charm.s3 import PaaSS3RelationData, PaaSS3Requirer

logger = logging.getLogger(__name__)


def public_url_environment(
    api_ingress_url: str | None,
    oci_ingress_url: str | None,
) -> dict[str, str]:
    """Map ingress relation URLs to the workload's public URL environment.

    The main ingress fronts both the API and artifact downloads, so its URL
    feeds the API and storage variables; the OCI ingress fronts the embedded
    OCI registry listener. Both relations are required, so the variables are
    always present once the charm is active; until then the charm blocks.
    """
    env: dict[str, str] = {}
    if api_ingress_url:
        public_url = api_ingress_url.rstrip("/")
        env["CHARM_REGISTRY_PUBLIC_API_URL"] = public_url
        env["CHARM_REGISTRY_PUBLIC_STORAGE_URL"] = public_url
    if oci_ingress_url:
        env["CHARM_REGISTRY_PUBLIC_REGISTRY_URL"] = oci_ingress_url.rstrip("/")
    return env


def size_limit_environment(config: typing.Mapping[str, typing.Any]) -> dict[str, str]:
    """Map charm byte-size config options to workload environment variables."""
    mapping = {
        "charmhub-max-artifact-bytes": "CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES",
        "max-archive-file-bytes": "CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES",
        "max-upload-bytes": "CHARM_REGISTRY_MAX_UPLOAD_BYTES",
    }
    env: dict[str, str] = {}
    for config_key, env_key in mapping.items():
        value = config.get(config_key, config.get(config_key.replace("-", "_")))
        if value is not None and str(value).strip():
            env[env_key] = str(value).strip()
    return env


def rate_limit_environment(config: typing.Mapping[str, typing.Any]) -> dict[str, str]:
    """Map charm rate-limit config options to workload environment variables."""
    mapping = {
        "rate-limit-ip-limit": "CHARM_REGISTRY_IP_RATE_LIMIT",
        "rate-limit-ip-window": "CHARM_REGISTRY_IP_RATE_WINDOW",
        "rate-limit-token-limit": "CHARM_REGISTRY_TOKEN_RATE_LIMIT",
        "rate-limit-token-window": "CHARM_REGISTRY_TOKEN_RATE_WINDOW",
    }
    env: dict[str, str] = {}
    for config_key, env_key in mapping.items():
        value = config.get(config_key, config.get(config_key.replace("-", "_")))
        if value is not None and str(value).strip():
            env[env_key] = str(value).strip()
    return env


class CharmRegistryApp(App):
    """Application runtime with an extra OCI S3 relation."""

    def __init__(
        self,
        *args: typing.Any,
        oci_s3: PaaSS3Requirer | None = None,
        api_ingress: IngressPerAppRequirer | None = None,
        oci_ingress: IngressPerAppRequirer | None = None,
        **kwargs: typing.Any,
    ) -> None:
        """Initialize the application runtime.

        Args:
            args: passthrough to App.
            oci_s3: S3 requirer for the embedded OCI registry bucket.
            api_ingress: ingress requirer fronting the API and storage endpoints.
            oci_ingress: ingress requirer for the embedded OCI registry listener.
            kwargs: passthrough to App.
        """
        super().__init__(*args, **kwargs)
        self._oci_s3 = oci_s3
        self._api_ingress = api_ingress
        self._oci_ingress = oci_ingress

    def _generate_integration_environments(self, prefix: str = "") -> dict[str, str]:
        """Generate workload environment, including OCI S3 relation data."""
        env = super()._generate_integration_environments(prefix=prefix)
        env.setdefault(prefix + "CHARM_REGISTRY_DATA_DIR", "data")
        if self._oci_s3:
            relation_data = self._oci_s3.to_relation_data()
            if relation_data:
                env.update(self._oci_s3_environment(relation_data, prefix=prefix))
        env.update(
            public_url_environment(
                self._api_ingress.url if self._api_ingress else None,
                self._oci_ingress.url if self._oci_ingress else None,
            )
        )
        env.update(size_limit_environment(self._charm_state.user_defined_config))
        env.update(rate_limit_environment(self._charm_state.user_defined_config))
        return env

    def _oci_s3_environment(
        self,
        relation_data: PaaSS3RelationData,
        prefix: str = "",
    ) -> dict[str, str]:
        """Map the OCI S3 relation to the registry's OCI storage environment."""
        values = {
            "CHARM_REGISTRY_OCI_S3_ACCESS_KEY": relation_data.access_key,
            "CHARM_REGISTRY_OCI_S3_SECRET_KEY": relation_data.secret_key,
            "CHARM_REGISTRY_OCI_S3_REGION": relation_data.region,
            "CHARM_REGISTRY_OCI_S3_BUCKET": relation_data.bucket,
            "CHARM_REGISTRY_OCI_S3_ENDPOINT": relation_data.endpoint,
            "CHARM_REGISTRY_OCI_S3_PREFIX": relation_data.path,
            "CHARM_REGISTRY_OCI_S3_USE_PATH_STYLE": (
                "true" if relation_data.addressing_style == "path" else None
            ),
        }
        return {prefix + key: value for key, value in values.items() if value}


class CharmRegistryCharm(paas_charm.go.Charm):
    """Go Charm service."""

    def __init__(self, *args: typing.Any) -> None:
        """Initialize the instance.

        Args:
            args: passthrough to CharmBase.
        """
        self._oci_s3: PaaSS3Requirer | None = None
        self._oci_ingress: IngressPerAppRequirer | None = None
        super().__init__(*args)
        self._oci_s3 = self._init_oci_s3()
        self._oci_ingress = self._init_oci_ingress()

    def _init_oci_s3(self) -> PaaSS3Requirer | None:
        """Initialize the OCI S3 relation."""
        requires = self.framework.meta.requires
        if "oci-s3" not in requires or requires["oci-s3"].interface_name != "s3":
            return None
        oci_s3 = PaaSS3Requirer(
            charm=self,
            relation_name="oci-s3",
            bucket_name=f"{self.app.name}-oci",
        )
        self.framework.observe(oci_s3.on.credentials_changed, self._on_s3_credential_changed)
        self.framework.observe(oci_s3.on.credentials_gone, self._on_s3_credential_gone)
        return oci_s3

    def _init_oci_ingress(self) -> IngressPerAppRequirer | None:
        """Initialize the OCI registry ingress relation."""
        requires = self.framework.meta.requires
        if "oci-ingress" not in requires or requires["oci-ingress"].interface_name != "ingress":
            return None
        oci_ingress = IngressPerAppRequirer(
            self,
            relation_name="oci-ingress",
            port=5000,
            strip_prefix=True,
        )
        self.framework.observe(oci_ingress.on.ready, self._on_ingress_ready)
        self.framework.observe(oci_ingress.on.revoked, self._on_ingress_revoked)
        return oci_ingress

    def restart(self, rerun_migrations: bool = False) -> None:
        """Restart the workload and publish OCI ingress requirements."""
        super().restart(rerun_migrations=rerun_migrations)
        if self._oci_ingress:
            self._oci_ingress.provide_ingress_requirements(port=5000)
            self.unit.set_ports(
                ops.Port(protocol="tcp", port=self._workload_config.port),
                ops.Port(protocol="tcp", port=5000),
            )

    def _create_app(self) -> CharmRegistryApp:
        """Build the application runtime."""
        charm_state = self._create_charm_state()
        return CharmRegistryApp(
            container=self._container,
            charm_state=charm_state,
            workload_config=self._workload_config,
            database_migration=self._database_migration,
            oci_s3=self._oci_s3,
            api_ingress=self._ingress,
            oci_ingress=self._oci_ingress,
        )

    def _missing_required_storage_integrations(
        self,
        requires: dict[str, typing.Any],
        charm_state: typing.Any,
    ) -> typing.Generator[typing.Any, None, None]:
        """Return missing required storage integrations."""
        yield from super()._missing_required_storage_integrations(requires, charm_state)
        if self._oci_s3 and not self._oci_s3.to_relation_data():
            if not requires["oci-s3"].optional:
                yield "oci-s3"

    def _missing_required_other_integrations(
        self,
        requires: dict[str, typing.Any],
        charm_state: typing.Any,
    ) -> typing.Generator[typing.Any, None, None]:
        """Return missing required non-storage integrations."""
        yield from super()._missing_required_other_integrations(requires, charm_state)
        # Both ingresses are mandatory: they are the sole source of the public
        # API/storage and OCI registry URLs handed to clients.
        if self._ingress and not self._ingress.is_ready():
            yield "ingress"
        if self._oci_ingress and not self._oci_ingress.is_ready():
            yield "oci-ingress"


if __name__ == "__main__":
    ops.main(CharmRegistryCharm)
