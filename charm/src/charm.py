#!/usr/bin/env python3
# Copyright 2026 gschiano
# See LICENSE file for licensing details.

"""Go Charm entrypoint."""

import logging
import typing

import ops

from charms.traefik_k8s.v2.ingress import IngressPerAppRequirer
from paas_charm.app import App
from paas_charm.s3 import PaaSS3RelationData, PaaSS3Requirer

import paas_charm.go

logger = logging.getLogger(__name__)


class CharmRegistryApp(App):
    """Application runtime with an extra OCI S3 relation."""

    def __init__(
        self,
        *args: typing.Any,
        oci_s3: PaaSS3Requirer | None = None,
        oci_ingress: IngressPerAppRequirer | None = None,
        **kwargs: typing.Any,
    ) -> None:
        """Initialize the application runtime.

        Args:
            args: passthrough to App.
            oci_s3: S3 requirer for the embedded OCI registry bucket.
            oci_ingress: ingress requirer for the embedded OCI registry listener.
            kwargs: passthrough to App.
        """
        super().__init__(*args, **kwargs)
        self._oci_s3 = oci_s3
        self._oci_ingress = oci_ingress

    def _generate_integration_environments(self, prefix: str = "") -> dict[str, str]:
        """Generate workload environment, including OCI S3 relation data."""
        env = super()._generate_integration_environments(prefix=prefix)
        if self._oci_s3:
            relation_data = self._oci_s3.to_relation_data()
            if relation_data:
                env.update(self._oci_s3_environment(relation_data, prefix=prefix))
        if self._oci_ingress and self._oci_ingress.url:
            env["CHARM_REGISTRY_PUBLIC_REGISTRY_URL"] = self._oci_ingress.url.rstrip("/")
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

    def restart(self, *, rerun_migrations: bool = False) -> None:
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
            oci_ingress=self._oci_ingress,
        )

    def _missing_required_storage_integrations(
        self,
        requires: dict[str, typing.Any],
        charm_state: typing.Any,
    ) -> typing.Iterator[str]:
        """Return missing required storage integrations."""
        yield from super()._missing_required_storage_integrations(requires, charm_state)
        if self._oci_s3 and not self._oci_s3.to_relation_data():
            if not requires["oci-s3"].optional:
                yield "oci-s3"

    def _missing_required_other_integrations(
        self,
        requires: dict[str, typing.Any],
        charm_state: typing.Any,
    ) -> typing.Iterator[str]:
        """Return missing required non-storage integrations."""
        yield from super()._missing_required_other_integrations(requires, charm_state)
        if self._oci_ingress and not self._oci_ingress.is_ready():
            if not requires["oci-ingress"].optional:
                yield "oci-ingress"


if __name__ == "__main__":
    ops.main(CharmRegistryCharm)
