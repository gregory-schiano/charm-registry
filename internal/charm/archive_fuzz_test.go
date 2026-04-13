package charm

import (
	"archive/zip"
	"bytes"
	"testing"
)

func FuzzParseArchive(f *testing.F) {
	f.Add([]byte("not a zip file"))
	f.Add(validArchiveSeed())

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
	})
}

func validArchiveSeed() []byte {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	file, err := writer.Create("metadata.yaml")
	if err != nil {
		panic(err)
	}
	if _, err := file.Write([]byte("name: fuzz-charm\n")); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
