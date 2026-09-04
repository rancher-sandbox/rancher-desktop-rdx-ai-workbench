package main

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// extractTar unpacks a tar stream into destination.
func extractTar(reader io.Reader, destination string) error {
	tarReader := tar.NewReader(reader)
	// Links are created after the loop, once the files they point at exist.
	var links []tar.Header
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar archive: %w", err)
		}
		if !filepath.IsLocal(header.Name) {
			return fmt.Errorf("error extracting archive: path %s: %w", header.Name, tar.ErrInsecurePath)
		}
		outPath := filepath.Join(destination, header.Name)
		info := header.FileInfo()
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(outPath, info.Mode()); err != nil {
				return fmt.Errorf("error extracting %s: failed to make directory: %w", header.Name, err)
			}
			if err = os.Chmod(outPath, header.FileInfo().Mode()); err != nil {
				return fmt.Errorf("error extracting %s: failed to change permissions: %w", header.Name, err)
			}
		case tar.TypeReg:
			if err = os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return fmt.Errorf("error extracting %s: failed to make directory: %w", header.Name, err)
			}
			file, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
			if err != nil {
				return fmt.Errorf("error extracting %s: failed to create file: %w", header.Name, err)
			}
			n, err := io.Copy(file, tarReader)
			file.Close()
			if err != nil {
				return fmt.Errorf("error extracting %s: failed to copy: %w", header.Name, err)
			}
			if n < header.Size {
				return fmt.Errorf("error extracting %s: extracted %d of %d bytes", header.Name, n, header.Size)
			}
		case tar.TypeLink, tar.TypeSymlink:
			if !filepath.IsLocal(header.Linkname) {
				return fmt.Errorf("error extracting %s: %w", header.Name, tar.ErrInsecurePath)
			}
			links = append(links, *header)
		default:
			return fmt.Errorf("error extracting %s: don't know how to handle %v", header.Name, header.Typeflag)
		}
	}

	for _, link := range links {
		newName := filepath.Join(destination, link.Name)
		oldName := filepath.Join(destination, link.Linkname)
		var err error
		if link.Typeflag == tar.TypeLink {
			err = os.Link(oldName, newName)
		} else {
			err = os.Symlink(oldName, newName)
		}
		if err != nil {
			return fmt.Errorf("error extracting %s: could not create link: %w", link.Name, err)
		}
	}

	return nil
}
