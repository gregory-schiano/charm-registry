package api

import (
	"strconv"
	"testing"
)

func FuzzParseCharmDownloadFilename(f *testing.F) {
	f.Add("package-id_1.charm")
	f.Add("pkg_0.charm")
	f.Add("invalid")
	f.Add("pkg_not-a-number.charm")

	f.Fuzz(func(t *testing.T, filename string) {
		packageID, revision, err := parseCharmDownloadFilename(filename)
		if err != nil {
			return
		}

		roundTripPackageID, roundTripRevision, roundTripErr := parseCharmDownloadFilename(
			packageID + "_" + strconv.Itoa(revision) + ".charm",
		)
		if roundTripErr != nil {
			t.Fatalf("round-trip parse failed: %v", roundTripErr)
		}
		if roundTripPackageID != packageID || roundTripRevision != revision {
			t.Fatalf("round-trip mismatch: got (%q, %d), want (%q, %d)",
				roundTripPackageID, roundTripRevision, packageID, revision)
		}
	})
}

func FuzzParseResourceDownloadFilename(f *testing.F) {
	f.Add("charm_package-id.config_1")
	f.Add("charm_pkg.resource_name_0")
	f.Add("invalid")
	f.Add("charm_pkg.resource_not-a-number")

	f.Fuzz(func(t *testing.T, filename string) {
		packageID, resourceName, revision, err := parseResourceDownloadFilename(filename)
		if err != nil {
			return
		}

		roundTripPackageID, roundTripResourceName, roundTripRevision, roundTripErr := parseResourceDownloadFilename(
			"charm_" + packageID + "." + resourceName + "_" + strconv.Itoa(revision),
		)
		if roundTripErr != nil {
			t.Fatalf("round-trip parse failed: %v", roundTripErr)
		}
		if roundTripPackageID != packageID || roundTripResourceName != resourceName || roundTripRevision != revision {
			t.Fatalf("round-trip mismatch: got (%q, %q, %d), want (%q, %q, %d)",
				roundTripPackageID, roundTripResourceName, roundTripRevision,
				packageID, resourceName, revision)
		}
	})
}
