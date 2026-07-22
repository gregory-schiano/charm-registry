package charm

import (
	"archive/zip"
	"bytes"
	"testing"
)

func FuzzParseArchive(f *testing.F) {
	f.Add([]byte("not a zip file"))
	f.Add(validArchiveSeed())
	f.Add(archiveSeed(map[string]string{
		"metadata.yaml": "name: fuzz-charm\ncontainers:\n  workload:\n    resource: workload-image\n",
	}))
	f.Add(archiveSeed(map[string]string{
		"metadata.yaml": "name: fuzz-charm\n",
		"manifest.yaml": "bases:\n  - name: ubuntu\n    channel: \"24.04\"\n    architectures:\n      - amd64\n",
	}))

	f.Fuzz(func(t *testing.T, payload []byte) {
		archive, err := ParseArchive(payload)
		if err != nil {
			return
		}
		if archive.MetadataYAML == "" {
			t.Fatal("expected metadata.yaml to be present on successful parse")
		}
		if archive.Manifest.Name == "" {
			t.Fatal("expected manifest name to be present on successful parse")
		}

		bounded, boundedErr := ParseArchiveWithMaxFileSize(payload, defaultMaxArchiveFileSize)
		if boundedErr != nil {
			t.Fatalf("bounded parse failed: %v", boundedErr)
		}
		if bounded.MetadataYAML != archive.MetadataYAML {
			t.Fatal("expected bounded parse to preserve metadata contents")
		}
		if bounded.Manifest.Name != archive.Manifest.Name {
			t.Fatalf("bounded parse name mismatch: got %q, want %q", bounded.Manifest.Name, archive.Manifest.Name)
		}
	})
}

func validArchiveSeed() []byte {
	return archiveSeed(map[string]string{"metadata.yaml": "name: fuzz-charm\n"})
}

func archiveSeed(files map[string]string) []byte {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			panic(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			panic(err)
		}
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
