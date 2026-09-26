package mermaid

import (
	"sort"
	"strconv"
	"strings"
)

// This file reproduces the Arabic lam-alef ligature rule of `unicode-width`
// 0.2.1's `UnicodeWidthStr::width`, which is what Rust's Mermaid renderer uses.
// `uniseg` (Go's string-width source) does not collapse the ligature, so without
// this `checkLabelText` accepted labels Rust rejects.

// lamAlefTransparentSpec is the crate's transparent-zero-width set
// (`is_transparent_zero_width`): the zero-width characters that do not interrupt
// the lam-alef ligature, compressed as inclusive hex ranges.
const lamAlefTransparentSpec = "00AD 0300-036F 0483-0489 0591-05BD 05BF 05C1-05C2 05C4-05C5 05C7 0610-061A 061C 064B-065F 0670 06D6-06DC 06DF-" +
	"06E4 06E7-06E8 06EA-06ED 070F 0711 0730-074A 07A6-07B0 07EB-07F3 07FD 0816-0819 081B-0823 0825-0827 0829-082D " +
	"0859-085B 0897-089F 08CA-08E1 08E3-0902 093A 093C 0941-0948 094D 0951-0957 0962-0963 0981 09BC 09C1-09C4 09CD " +
	"09E2-09E3 09FE 0A01-0A02 0A3C 0A41-0A42 0A47-0A48 0A4B-0A4D 0A51 0A70-0A71 0A75 0A81-0A82 0ABC 0AC1-0AC5 0AC7-" +
	"0AC8 0ACD 0AE2-0AE3 0AFA-0AFF 0B01 0B3C 0B3F 0B41-0B44 0B4D 0B55-0B56 0B62-0B63 0B82 0BC0 0BCD 0C00 0C04 0C3C " +
	"0C3E-0C40 0C46-0C48 0C4A-0C4D 0C55-0C56 0C62-0C63 0C81 0CBC 0CBF 0CC6 0CCC-0CCD 0CE2-0CE3 0D00-0D01 0D3B-0D3C " +
	"0D41-0D44 0D4D 0D62-0D63 0D81 0DCA 0DD2-0DD4 0DD6 0E31 0E34-0E3A 0E47-0E4E 0EB1 0EB4-0EBC 0EC8-0ECE 0F18-0F19 " +
	"0F35 0F37 0F39 0F71-0F7E 0F80-0F84 0F86-0F87 0F8D-0F97 0F99-0FBC 0FC6 102D-1030 1032-1037 1039-103A 103D-103E " +
	"1058-1059 105E-1060 1071-1074 1082 1085-1086 108D 109D 135D-135F 1712-1714 1732-1733 1752-1753 1772-1773 17B4-" +
	"17B5 17B7-17BD 17C6 17C9-17D3 17DD 180B-180D 180F 1885-1886 18A9 1920-1922 1927-1928 1932 1939-193B 1A17-1A18 " +
	"1A1B 1A56 1A58-1A5E 1A60 1A62 1A65-1A6C 1A73-1A7C 1A7F 1AB0-1ACE 1B00-1B03 1B34 1B36-1B3A 1B3C 1B42 1B6B-1B73 " +
	"1B80-1B81 1BA2-1BA5 1BA8-1BA9 1BAB-1BAD 1BE6 1BE8-1BE9 1BED 1BEF-1BF1 1C2C-1C33 1C36-1C37 1CD0-1CD2 1CD4-1CE0 " +
	"1CE2-1CE8 1CED 1CF4 1CF8-1CF9 1DC0-1DFF 200B 200E-200F 202A-202E 2060-2064 206A-206F 20D0-20F0 2CEF-2CF1 2DE0-" +
	"2DFF 302A-302D 3099-309A A66F-A672 A674-A67D A69E-A69F A6F0-A6F1 A802 A806 A80B A825-A826 A82C A8C4-A8C5 A8E0-" +
	"A8F1 A8FF A926-A92D A947-A951 A980-A982 A9B3 A9B6-A9B9 A9BC-A9BD A9E5 AA29-AA2E AA31-AA32 AA35-AA36 AA43 AA4C " +
	"AA7C AAB0 AAB2-AAB4 AAB7-AAB8 AABE-AABF AAC1 AAEC-AAED AAF6 ABE5 ABE8 ABED FB1E FE00-FE0F FE20-FE2F FEFF 101FD" +
	" 102E0 10376-1037A 10A01-10A03 10A05-10A06 10A0C-10A0F 10A38-10A3A 10A3F 10AE5-10AE6 10D24-10D27 10D69-10D6D 1" +
	"0EAB-10EAC 10EFC-10EFF 10F46-10F50 10F82-10F85 11001 11038-11046 11070 11073-11074 1107F-11081 110B3-110B6 110" +
	"B9-110BA 110C2 11100-11102 11127-1112B 1112D-11134 11173 11180-11181 111B6-111BE 111C9-111CC 111CF 1122F-11231" +
	" 11234 11236-11237 1123E 11241 112DF 112E3-112EA 11300-11301 1133B-1133C 11340 11366-1136C 11370-11374 113BB-1" +
	"13C0 113CE 113D0 113D2 113E1-113E2 11438-1143F 11442-11444 11446 1145E 114B3-114B8 114BA 114BF-114C0 114C2-114" +
	"C3 115B2-115B5 115BC-115BD 115BF-115C0 115DC-115DD 11633-1163A 1163D 1163F-11640 116AB 116AD 116B0-116B5 116B7" +
	" 1171D 1171F 11722-11725 11727-1172B 1182F-11837 11839-1183A 1193B-1193C 1193E 11943 119D4-119D7 119DA-119DB 1" +
	"19E0 11A01-11A0A 11A33-11A38 11A3B-11A3E 11A47 11A51-11A56 11A59-11A5B 11A8A-11A96 11A98-11A99 11C30-11C36 11C" +
	"38-11C3D 11C3F 11C92-11CA7 11CAA-11CB0 11CB2-11CB3 11CB5-11CB6 11D31-11D36 11D3A 11D3C-11D3D 11D3F-11D45 11D47" +
	" 11D90-11D91 11D95 11D97 11EF3-11EF4 11F00-11F01 11F36-11F3A 11F40 11F42 11F5A 13440 13447-13455 1611E-16129 1" +
	"612D-1612F 16AF0-16AF4 16B30-16B36 16F4F 16F8F-16F92 16FE4 1BC9D-1BC9E 1BCA0-1BCA3 1CF00-1CF2D 1CF30-1CF46 1D1" +
	"67-1D169 1D173-1D182 1D185-1D18B 1D1AA-1D1AD 1D242-1D244 1DA00-1DA36 1DA3B-1DA6C 1DA75 1DA84 1DA9B-1DA9F 1DAA1" +
	"-1DAAF 1E000-1E006 1E008-1E018 1E01B-1E021 1E023-1E024 1E026-1E02A 1E08F 1E130-1E136 1E2AE 1E2EC-1E2EF 1E4EC-1" +
	"E4EF 1E5EE-1E5EF 1E8D0-1E8D6 1E944-1E94A E0001 E0020-E007F E0100-E01EF"

// lamJoiningGroup is unicode-width's Arabic lam-alef arm
// (Joining_Group=Lam): U+0644, U+06B5..U+06B8, U+076A, U+08A6, U+08C7.
var lamJoiningGroup = [][2]rune{
	{0x0644, 0x0644},
	{0x06B5, 0x06B8},
	{0x076A, 0x076A},
	{0x08A6, 0x08A6},
	{0x08C7, 0x08C7},
}

// alefJoiningGroup is unicode-width's JOINING_GROUP_ALEF set: U+0622..U+0623,
// U+0625, U+0627, U+0671..U+0673, U+0675, U+0773..U+0774, U+0870..U+0882.
var alefJoiningGroup = [][2]rune{
	{0x0622, 0x0623},
	{0x0625, 0x0625},
	{0x0627, 0x0627},
	{0x0671, 0x0673},
	{0x0675, 0x0675},
	{0x0773, 0x0774},
	{0x0870, 0x0882},
}

var lamAlefTransparent = parseRuneRanges(lamAlefTransparentSpec)

// lamAlefCollapses counts the Arabic lam-alef ligatures unicode-width 0.2.1
// collapses in text: a Lam-joining-group character followed, after any run of
// transparent zero-width characters, by an Alef-joining-group character has
// total width 1 instead of 2.
func lamAlefCollapses(text string) int {
	runes := []rune(text)
	collapses := 0
	for i := 0; i < len(runes); i++ {
		if !inRuneRanges(runes[i], lamJoiningGroup) {
			continue
		}
		j := i + 1
		for j < len(runes) && inRuneRanges(runes[j], lamAlefTransparent) {
			j++
		}
		if j < len(runes) && inRuneRanges(runes[j], alefJoiningGroup) {
			collapses++
		}
	}
	return collapses
}

func parseRuneRanges(spec string) [][2]rune {
	fields := strings.Fields(spec)
	ranges := make([][2]rune, 0, len(fields))
	for _, field := range fields {
		lo, hi, found := strings.Cut(field, "-")
		start, err := strconv.ParseUint(lo, 16, 32)
		if err != nil {
			continue
		}
		end := start
		if found {
			if parsed, err := strconv.ParseUint(hi, 16, 32); err == nil {
				end = parsed
			}
		}
		ranges = append(ranges, [2]rune{rune(start), rune(end)})
	}
	return ranges
}

func inRuneRanges(c rune, ranges [][2]rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= c })
	return i < len(ranges) && c >= ranges[i][0]
}
