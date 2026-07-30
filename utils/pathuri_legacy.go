package utils

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf16"
)

type LegacyAppPathString struct {
	Value string
}

type LegacyError struct {
	Reason     string
	Path       string
	Convention PathConvention
}

func (e *LegacyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Convention != "" {
		return fmt.Sprintf("%s: %s using %s", e.Reason, e.Path, e.Convention)
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Path)
}

func NewLegacyAppPathString(value string) LegacyAppPathString {
	return LegacyAppPathString{Value: value}
}

func LegacyAppPathStringFromURI(uri *PathURI, convention PathConvention) (LegacyAppPathString, error) {
	if uri == nil || uri.url == nil {
		return LegacyAppPathString{}, &LegacyError{Reason: "path URI is nil"}
	}
	if data := opaqueFallbackBytes(uri.url); data != nil {
		if convention == ConventionWindows && len(data)%2 == 0 {
			values := make([]uint16, len(data)/2)
			for i := range values {
				values[i] = uint16(data[i*2]) | uint16(data[i*2+1])<<8
			}
			return LegacyAppPathString{Value: string(utf16.Decode(values))}, nil
		}
		if convention == ConventionPosix {
			return LegacyAppPathString{Value: string(data)}, nil
		}
	}
	switch convention {
	case ConventionPosix:
		if uri.url.Host != "" {
			return LegacyAppPathString{}, &LegacyError{Reason: "path URI cannot be rendered using POSIX path syntax", Path: uri.String(), Convention: convention}
		}
		segments := uri.segments()
		if len(segments) == 0 {
			return LegacyAppPathString{Value: "/"}, nil
		}
		decoded := make([]string, 0, len(segments))
		for _, segment := range segments {
			decoded = append(decoded, decodeSegmentLossy(segment))
		}
		value := "/" + strings.Join(decoded, "/")
		if strings.HasSuffix(uri.url.EscapedPath(), "/") && !strings.HasSuffix(value, "/") {
			value += "/"
		}
		return LegacyAppPathString{Value: value}, nil
	case ConventionWindows:
		segments := uri.segments()
		if uri.url.Host != "" {
			if len(segments) == 0 {
				return LegacyAppPathString{}, &LegacyError{Reason: "path URI cannot be rendered using Windows path syntax", Path: uri.String(), Convention: convention}
			}
			decoded := make([]string, 0, len(segments))
			for _, segment := range segments {
				decoded = append(decoded, decodeSegmentLossy(segment))
			}
			value := `\\` + uri.url.Host + `\` + strings.Join(decoded, `\`)
			if strings.HasSuffix(uri.url.EscapedPath(), "/") && !strings.HasSuffix(value, `\`) {
				value += `\`
			}
			return LegacyAppPathString{Value: value}, nil
		}
		if len(segments) == 0 || !isWindowsDriveSegment(segments[0]) {
			return LegacyAppPathString{}, &LegacyError{Reason: "path URI cannot be rendered using Windows path syntax", Path: uri.String(), Convention: convention}
		}
		decoded := make([]string, 0, len(segments))
		for _, segment := range segments {
			decoded = append(decoded, decodeSegmentLossy(segment))
		}
		value := decoded[0] + `\`
		if len(decoded) > 1 {
			value += strings.Join(decoded[1:], `\`)
		}
		if strings.HasSuffix(uri.url.EscapedPath(), "/") && !strings.HasSuffix(value, `\`) {
			value += `\`
		}
		return LegacyAppPathString{Value: value}, nil
	default:
		return LegacyAppPathString{}, &LegacyError{Reason: "unknown path convention", Convention: convention}
	}
}

func (p *LegacyAppPathString) ToPathURI(convention PathConvention) (*PathURI, error) {
	if p == nil {
		return nil, &LegacyError{Reason: "path is nil"}
	}
	return FromAbsoluteNativePath(p.Value, convention)
}

func (p *LegacyAppPathString) InferAbsolutePathConvention() (PathConvention, bool) {
	if p == nil {
		return "", false
	}
	bytes := []byte(p.Value)
	if len(bytes) >= 3 && pathURIASCIIAlpha(bytes[0]) && bytes[1] == ':' && (bytes[2] == '\\' || bytes[2] == '/') {
		return ConventionWindows, true
	}
	if strings.HasPrefix(p.Value, `\\`) {
		return ConventionWindows, true
	}
	if strings.HasPrefix(p.Value, "/") {
		return ConventionPosix, true
	}
	return "", false
}

func (p *LegacyAppPathString) ToInferredPathURI() (*PathURI, bool) {
	convention, ok := p.InferAbsolutePathConvention()
	if !ok {
		return nil, false
	}
	uri, err := p.ToPathURI(convention)
	if err != nil {
		return nil, false
	}
	return uri, true
}

func (p *LegacyAppPathString) RenderForUI() string {
	if p == nil {
		return ""
	}
	uri, ok := p.ToInferredPathURI()
	if !ok {
		return p.Value
	}
	return uri.NativePathString()
}

func decodeSegmentLossy(segment string) string {
	decoded, err := url.PathUnescape(segment)
	if err != nil {
		return segment
	}
	return decoded
}
