# Copyright 2026 gschiano
# See LICENSE file for licensing details.

"""Unit tests for the charm's environment mapping."""

from charm import public_url_environment, rate_limit_environment, size_limit_environment


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


def test_size_limits_map_to_registry_environment():
    env = size_limit_environment(
        {
            "charmhub_max_artifact_bytes": "128MB",
            "max_archive_file_bytes": "32MB",
            "max_upload_bytes": "1GB",
        }
    )

    assert env == {
        "CHARM_REGISTRY_CHARMHUB_MAX_ARTIFACT_BYTES": "128MB",
        "CHARM_REGISTRY_MAX_ARCHIVE_FILE_BYTES": "32MB",
        "CHARM_REGISTRY_MAX_UPLOAD_BYTES": "1GB",
    }


def test_size_limits_skip_empty_values():
    env = size_limit_environment(
        {
            "charmhub-max-artifact-bytes": "",
            "max-archive-file-bytes": None,
            "max-upload-bytes": " 128MB ",
        }
    )

    assert env == {
        "CHARM_REGISTRY_MAX_UPLOAD_BYTES": "128MB",
    }


def test_rate_limits_map_to_registry_environment():
    env = rate_limit_environment(
        {
            "rate_limit_ip_limit": 0,
            "rate_limit_ip_window": "30s",
            "rate_limit_token_limit": 10,
            "rate_limit_token_window": "2m",
        }
    )

    assert env == {
        "CHARM_REGISTRY_IP_RATE_LIMIT": "0",
        "CHARM_REGISTRY_IP_RATE_WINDOW": "30s",
        "CHARM_REGISTRY_TOKEN_RATE_LIMIT": "10",
        "CHARM_REGISTRY_TOKEN_RATE_WINDOW": "2m",
    }
