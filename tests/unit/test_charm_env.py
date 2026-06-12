# Copyright 2026 gschiano
# See LICENSE file for licensing details.

"""Unit tests for the charm's ingress-to-environment mapping."""

from charm import public_url_environment


def test_no_ingress_yields_no_overrides():
    assert public_url_environment(None, None) == {}


def test_api_ingress_sets_api_and_storage_urls():
    env = public_url_environment("https://registry.example.com/", None)
    assert env == {
        "CHARM_REGISTRY_PUBLIC_API_URL": "https://registry.example.com",
        "CHARM_REGISTRY_PUBLIC_STORAGE_URL": "https://registry.example.com",
    }


def test_oci_ingress_sets_registry_url_only():
    env = public_url_environment(None, "https://oci.example.com/model-app/")
    assert env == {
        "CHARM_REGISTRY_PUBLIC_REGISTRY_URL": "https://oci.example.com/model-app",
    }


def test_both_ingresses_set_all_three_urls():
    env = public_url_environment(
        "https://registry.example.com",
        "https://oci.example.com",
    )
    assert env == {
        "CHARM_REGISTRY_PUBLIC_API_URL": "https://registry.example.com",
        "CHARM_REGISTRY_PUBLIC_STORAGE_URL": "https://registry.example.com",
        "CHARM_REGISTRY_PUBLIC_REGISTRY_URL": "https://oci.example.com",
    }
