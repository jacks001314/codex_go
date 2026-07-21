package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginBundlePackError represents an error when packing a plugin bundle.
type PluginBundlePackError struct {
	message string
}

func (e *PluginBundlePackError) Error() string {
	return e.message
}

// PluginBundleUnpackError represents an error when unpacking a plugin bundle.
type PluginBundleUnpackError struct {
	message string
}

func (e *PluginBundleUnpackError) Error() string {
	return e.message
}

// PackPluginBundleTarGz creates a gzipped tar archive from a plugin directory.
// It enforces a maximum archive size and validates the plugin structure.
//
// pluginPath must be a directory containing .codex-plugin/plugin.json.
// maxBytes is the maximum allowed archive size in bytes.
func PackPluginBundleTarGz(pluginPath string, maxBytes int) ([]byte, error) {
	info, err := os.Stat(pluginPath)
	if err != nil || !info.IsDir() {
		return nil, &PluginBundlePackError{
			message: fmt.Sprintf("invalid plugin path %q: expected a plugin directory", pluginPath),
		}
	}

	manifestPath := filepath.Join(pluginPath, ".codex-plugin", "plugin.json")
	if _, err := os.Stat(manifestPath); err != nil {
		return nil, &PluginBundlePackError{
			message: fmt.Sprintf("invalid plugin path %q: missing .codex-plugin/plugin.json", pluginPath),
		}
	}

	buf := &sizeLimitedBuffer{maxBytes: maxBytes}
	gzWriter := gzip.NewWriter(buf)
	tarWriter := tar.NewWriter(gzWriter)

	if err := appendPluginTree(tarWriter, pluginPath, pluginPath); err != nil {
		tarWriter.Close()
		gzWriter.Close()
		if archiveErr, ok := err.(*archiveSizeLimitExceeded); ok {
			return nil, &PluginBundlePackError{
				message: fmt.Sprintf("plugin archive would be %d bytes, exceeding maximum size of %d bytes",
					archiveErr.bytes, archiveErr.maxBytes),
			}
		}
		return nil, &PluginBundlePackError{
			message: fmt.Sprintf("failed to archive plugin bundle: %v", err),
		}
	}

	if err := tarWriter.Close(); err != nil {
		gzWriter.Close()
		return nil, &PluginBundlePackError{
			message: fmt.Sprintf("failed to finalize tar archive: %v", err),
		}
	}

	if err := gzWriter.Close(); err != nil {
		return nil, &PluginBundlePackError{
			message: fmt.Sprintf("failed to finalize gzip compression: %v", err),
		}
	}

	return buf.data, nil
}

func appendPluginTree(tarWriter *tar.Writer, pluginRoot string, current string) error {
	entries, err := os.ReadDir(current)
	if err != nil {
		return err
	}

	// Sort entries by name for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		fullPath := filepath.Join(current, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(pluginRoot, fullPath)
		if err != nil {
			return fmt.Errorf("failed to compute plugin archive path for %q: %w", fullPath, err)
		}

		// Convert Windows paths to forward slashes for tar
		relPath = filepath.ToSlash(relPath)

		if entry.IsDir() {
			header := &tar.Header{
				Name:     relPath + "/",
				Mode:     int64(info.Mode()),
				ModTime:  info.ModTime(),
				Typeflag: tar.TypeDir,
			}
			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}
			if err := appendPluginTree(tarWriter, pluginRoot, fullPath); err != nil {
				return err
			}
		} else if info.Mode().IsRegular() {
			header := &tar.Header{
				Name:     relPath,
				Size:     info.Size(),
				Mode:     int64(info.Mode()),
				ModTime:  info.ModTime(),
				Typeflag: tar.TypeReg,
			}
			if err := tarWriter.WriteHeader(header); err != nil {
				return err
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				return err
			}
			if _, err := tarWriter.Write(data); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("unsupported plugin archive entry type: %s", fullPath)
		}
	}

	return nil
}

// UnpackPluginBundleTarGz decompresses and extracts a gzipped tar bundle.
// It enforces a maximum total extracted size and protects against path traversal.
func UnpackPluginBundleTarGz(data []byte, destination string, maxExtractedBytes int64) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return &PluginBundleUnpackError{
			message: fmt.Sprintf("failed to create plugin bundle extraction directory: %v", err),
		}
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return &PluginBundleUnpackError{
			message: fmt.Sprintf("failed to read plugin bundle tar: %v", err),
		}
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	var totalExtracted int64
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return &PluginBundleUnpackError{
				message: fmt.Sprintf("failed to read plugin bundle tar entry: %v", err),
			}
		}

		outputPath, err := checkedTarOutputPathForBundle(destination, header.Name)
		if err != nil {
			return &PluginBundleUnpackError{message: err.Error()}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(outputPath, 0o755); err != nil {
				return &PluginBundleUnpackError{
					message: fmt.Sprintf("failed to create plugin bundle directory: %v", err),
				}
			}

		case tar.TypeReg:
			// Enforce size limit
			if totalExtracted+header.Size > maxExtractedBytes {
				return &PluginBundleUnpackError{
					message: fmt.Sprintf("plugin bundle extracted size would be %d bytes, exceeding maximum total size of %d bytes",
						totalExtracted+header.Size, maxExtractedBytes),
				}
			}

			parent := filepath.Dir(outputPath)
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return &PluginBundleUnpackError{
					message: fmt.Sprintf("failed to create plugin bundle directory: %v", err),
				}
			}

			f, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return &PluginBundleUnpackError{
					message: fmt.Sprintf("failed to unpack plugin bundle entry: %v", err),
				}
			}

			written, err := io.CopyN(f, tarReader, header.Size)
			f.Close()
			if err != nil {
				return &PluginBundleUnpackError{
					message: fmt.Sprintf("failed to unpack plugin bundle entry: %v", err),
				}
			}
			totalExtracted += written

		case tar.TypeLink, tar.TypeSymlink:
			return &PluginBundleUnpackError{
				message: fmt.Sprintf("plugin bundle tar entry %q is a link", header.Name),
			}

		default:
			return &PluginBundleUnpackError{
				message: fmt.Sprintf("plugin bundle tar entry %q has unsupported type %d", header.Name, header.Typeflag),
			}
		}
	}

	return nil
}

func checkedTarOutputPathForBundle(destination string, entryName string) (string, error) {
	destination = filepath.Clean(destination)

	// Convert tar path (forward slashes) to OS-native path
	entryName = filepath.FromSlash(entryName)
	entryName = filepath.Clean(entryName)

	// Reject absolute paths
	if filepath.IsAbs(entryName) {
		return "", fmt.Errorf("plugin bundle tar entry %q escapes extraction root", entryName)
	}

	// Build output path and check for escapes
	outputPath := filepath.Join(destination, entryName)
	outputPath = filepath.Clean(outputPath)

	// Validate that the path is still within destination
	rel, err := filepath.Rel(destination, outputPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("plugin bundle tar entry %q escapes extraction root", entryName)
	}

	// Check that we have at least one path component
	if rel == "." || rel == "" {
		return "", fmt.Errorf("plugin bundle tar entry has an empty path")
	}

	return outputPath, nil
}

// sizeLimitedBuffer is a bytes.Buffer that enforces a maximum size.
type sizeLimitedBuffer struct {
	data     []byte
	maxBytes int
}

func (b *sizeLimitedBuffer) Write(p []byte) (int, error) {
	nextLen := len(b.data) + len(p)
	if nextLen > b.maxBytes {
		return 0, &archiveSizeLimitExceeded{
			bytes:    nextLen,
			maxBytes: b.maxBytes,
		}
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

// archiveSizeLimitExceeded is an error indicating that an archive exceeded its size limit.
type archiveSizeLimitExceeded struct {
	bytes    int
	maxBytes int
}

func (e *archiveSizeLimitExceeded) Error() string {
	return fmt.Sprintf("archive would be %d bytes, exceeding maximum size of %d bytes", e.bytes, e.maxBytes)
}
