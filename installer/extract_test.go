package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// tarball builds an archive in memory; regular files hold their own name.
func tarball(t *testing.T, headers ...tar.Header) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for _, header := range headers {
		if header.Typeflag == tar.TypeReg {
			header.Size = int64(len(header.Name))
		}
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatalf("failed to write header %s: %v", header.Name, err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(header.Name)); err != nil {
				t.Fatalf("failed to write %s: %v", header.Name, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to write archive: %v", err)
	}
	return bytes.NewReader(buf.Bytes())
}

// The layout mirrors the ollama archive, where the shared libraries are loaded
// through their soname, which is a symlink next to the real file.
func TestExtractTar(t *testing.T) {
	destination := t.TempDir()
	archive := tarball(t,
		tar.Header{Typeflag: tar.TypeDir, Name: "lib/", Mode: 0o755},
		tar.Header{Typeflag: tar.TypeReg, Name: "lib/libggml.so.0.22.0", Mode: 0o644},
		tar.Header{Typeflag: tar.TypeSymlink, Name: "lib/libggml.so.0", Linkname: "libggml.so.0.22.0"},
		tar.Header{Typeflag: tar.TypeDir, Name: "lib/cuda/", Mode: 0o755},
		tar.Header{Typeflag: tar.TypeSymlink, Name: "lib/cuda/libggml.so.0", Linkname: "../libggml.so.0.22.0"},
		tar.Header{Typeflag: tar.TypeReg, Name: "bin/ollama", Mode: 0o755},
		tar.Header{Typeflag: tar.TypeLink, Name: "bin/ollama-runner", Linkname: "bin/ollama"},
	)

	if err := extractTar(archive, destination); err != nil {
		t.Fatalf("failed to extract archive: %v", err)
	}

	for _, name := range []string{"lib/libggml.so.0", "lib/cuda/libggml.so.0"} {
		contents, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil {
			t.Errorf("failed to read %s: %v", name, err)
		} else if string(contents) != "lib/libggml.so.0.22.0" {
			t.Errorf("%s resolves to %q", name, contents)
		}
	}

	contents, err := os.ReadFile(filepath.Join(destination, "bin/ollama-runner"))
	if err != nil {
		t.Errorf("failed to read hard link: %v", err)
	} else if string(contents) != "bin/ollama" {
		t.Errorf("hard link holds %q", contents)
	}

	info, err := os.Stat(filepath.Join(destination, "bin/ollama"))
	if err != nil {
		t.Fatalf("failed to stat bin/ollama: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("bin/ollama has mode %v and cannot be run", info.Mode())
	}
}

func TestExtractTarRejectsPathsOutsideDestination(t *testing.T) {
	cases := map[string]tar.Header{
		"file":             {Typeflag: tar.TypeReg, Name: "../escape"},
		"hard link":        {Typeflag: tar.TypeLink, Name: "escape", Linkname: "../outside"},
		"symlink":          {Typeflag: tar.TypeSymlink, Name: "lib/escape", Linkname: "../../outside"},
		"absolute symlink": {Typeflag: tar.TypeSymlink, Name: "lib/escape", Linkname: "/etc/passwd"},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			err := extractTar(tarball(t, header), t.TempDir())
			if !errors.Is(err, tar.ErrInsecurePath) {
				t.Errorf("extracted %s %q pointing outside the destination: %v", name, header.Name, err)
			}
		})
	}
}
