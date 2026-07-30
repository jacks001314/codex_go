package execserver

// LocalFileSystem exposes the same filesystem operations used by the wire
// protocol so in-process capability consumers cannot bypass sandbox handling.
type LocalFileSystem struct{}

func (LocalFileSystem) ReadFile(params *FSReadFileParams) (*FSReadFileResponse, error) {
	return readFile(params)
}

func (LocalFileSystem) GetMetadata(params *FSGetMetadataParams) (*FSGetMetadataResponse, error) {
	return getMetadata(params)
}

func (LocalFileSystem) Walk(params *FSWalkParams) (*FSWalkResponse, error) {
	return walkPath(params)
}
