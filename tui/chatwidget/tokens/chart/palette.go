package chart

type Palette struct {
	Empty string
	Full  string
}

func DefaultPalette() Palette {
	return Palette{Empty: "□", Full: "■"}
}
