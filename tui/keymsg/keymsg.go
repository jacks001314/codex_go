// Package keymsg provides the TUI's internal key event model.
//
// The key type and constants are derived from Bubble Tea v1's key.go (MIT),
// which the Codex Go TUI's keymap and handlers were written against. Bubble
// Tea v2 replaced the struct with KeyPressMsg/KeyReleaseMsg; FromTea performs
// that conversion once at each tea.Model boundary so the rest of the TUI keeps
// one stable representation. Original retains the v2 message for Bubbles
// components (textarea/viewport) that need the native event.
package keymsg

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type KeyMsg Key

// String returns a string representation for a key message. It's safe (and
// encouraged) for use in key comparison.
func (k KeyMsg) String() (str string) {
	return Key(k).String()
}

// Key contains information about a keypress.
type Key struct {
	Type  KeyType
	Runes []rune
	Alt   bool
	Paste bool

	Mod      tea.KeyMod
	Code     rune
	Text     string
	Super    bool
	Original tea.Msg
}

// String returns a friendly string representation for a key. It's safe (and
// encouraged) for use in key comparison.
//
//	k := Key{Type: KeyEnter}
//	fmt.Println(k)
//	// Output: enter
func (k Key) String() (str string) {
	var buf strings.Builder
	if k.Alt {
		buf.WriteString("alt+")
	}
	if k.Type == KeyRunes {
		if k.Paste {
			// Note: bubbles/keys bindings currently do string compares to
			// recognize shortcuts. Since pasted text should never activate
			// shortcuts, we need to ensure that the binding code doesn't
			// match Key events that result from pastes. We achieve this
			// here by enclosing pastes in '[...]' so that the string
			// comparison in Matches() fails in that case.
			buf.WriteByte('[')
		}
		buf.WriteString(string(k.Runes))
		if k.Paste {
			buf.WriteByte(']')
		}
		return buf.String()
	} else if s, ok := keyNames[k.Type]; ok {
		buf.WriteString(s)
		return buf.String()
	}
	return ""
}

// KeyType indicates the key pressed, such as KeyEnter or KeyBreak or KeyCtrlC.
// All other keys will be type KeyRunes. To get the rune value, check the Rune
// method on a Key struct, or use the Key.String() method:
//
//	k := Key{Type: KeyRunes, Runes: []rune{'a'}, Alt: true}
//	if k.Type == KeyRunes {
//
//	    fmt.Println(k.Runes)
//	    // Output: a
//
//	    fmt.Println(k.String())
//	    // Output: alt+a
//
//	}
type KeyType int

func (k KeyType) String() (str string) {
	if s, ok := keyNames[k]; ok {
		return s
	}
	return ""
}

// Control keys. We could do this with an iota, but the values are very
// specific, so we set the values explicitly to avoid any confusion.
//
// See also:
// https://en.wikipedia.org/wiki/C0_and_C1_control_codes
const (
	keyNUL KeyType = 0   // null, \0
	keySOH KeyType = 1   // start of heading
	keySTX KeyType = 2   // start of text
	keyETX KeyType = 3   // break, ctrl+c
	keyEOT KeyType = 4   // end of transmission
	keyENQ KeyType = 5   // enquiry
	keyACK KeyType = 6   // acknowledge
	keyBEL KeyType = 7   // bell, \a
	keyBS  KeyType = 8   // backspace
	keyHT  KeyType = 9   // horizontal tabulation, \t
	keyLF  KeyType = 10  // line feed, \n
	keyVT  KeyType = 11  // vertical tabulation \v
	keyFF  KeyType = 12  // form feed \f
	keyCR  KeyType = 13  // carriage return, \r
	keySO  KeyType = 14  // shift out
	keySI  KeyType = 15  // shift in
	keyDLE KeyType = 16  // data link escape
	keyDC1 KeyType = 17  // device control one
	keyDC2 KeyType = 18  // device control two
	keyDC3 KeyType = 19  // device control three
	keyDC4 KeyType = 20  // device control four
	keyNAK KeyType = 21  // negative acknowledge
	keySYN KeyType = 22  // synchronous idle
	keyETB KeyType = 23  // end of transmission block
	keyCAN KeyType = 24  // cancel
	keyEM  KeyType = 25  // end of medium
	keySUB KeyType = 26  // substitution
	keyESC KeyType = 27  // escape, \e
	keyFS  KeyType = 28  // file separator
	keyGS  KeyType = 29  // group separator
	keyRS  KeyType = 30  // record separator
	keyUS  KeyType = 31  // unit separator
	keyDEL KeyType = 127 // delete. on most systems this is mapped to backspace, I hear
)

// Control key aliases.
const (
	KeyNull      KeyType = keyNUL
	KeyBreak     KeyType = keyETX
	KeyEnter     KeyType = keyCR
	KeyBackspace KeyType = keyDEL
	KeyTab       KeyType = keyHT
	KeyEsc       KeyType = keyESC
	KeyEscape    KeyType = keyESC

	KeyCtrlAt           KeyType = keyNUL // ctrl+@
	KeyCtrlA            KeyType = keySOH
	KeyCtrlB            KeyType = keySTX
	KeyCtrlC            KeyType = keyETX
	KeyCtrlD            KeyType = keyEOT
	KeyCtrlE            KeyType = keyENQ
	KeyCtrlF            KeyType = keyACK
	KeyCtrlG            KeyType = keyBEL
	KeyCtrlH            KeyType = keyBS
	KeyCtrlI            KeyType = keyHT
	KeyCtrlJ            KeyType = keyLF
	KeyCtrlK            KeyType = keyVT
	KeyCtrlL            KeyType = keyFF
	KeyCtrlM            KeyType = keyCR
	KeyCtrlN            KeyType = keySO
	KeyCtrlO            KeyType = keySI
	KeyCtrlP            KeyType = keyDLE
	KeyCtrlQ            KeyType = keyDC1
	KeyCtrlR            KeyType = keyDC2
	KeyCtrlS            KeyType = keyDC3
	KeyCtrlT            KeyType = keyDC4
	KeyCtrlU            KeyType = keyNAK
	KeyCtrlV            KeyType = keySYN
	KeyCtrlW            KeyType = keyETB
	KeyCtrlX            KeyType = keyCAN
	KeyCtrlY            KeyType = keyEM
	KeyCtrlZ            KeyType = keySUB
	KeyCtrlOpenBracket  KeyType = keyESC // ctrl+[
	KeyCtrlBackslash    KeyType = keyFS  // ctrl+\
	KeyCtrlCloseBracket KeyType = keyGS  // ctrl+]
	KeyCtrlCaret        KeyType = keyRS  // ctrl+^
	KeyCtrlUnderscore   KeyType = keyUS  // ctrl+_
	KeyCtrlQuestionMark KeyType = keyDEL // ctrl+?
)

// Other keys.
const (
	KeyRunes KeyType = -(iota + 1)
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyShiftTab
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDown
	KeyCtrlPgUp
	KeyCtrlPgDown
	KeyDelete
	KeyInsert
	KeySpace
	KeyCtrlUp
	KeyCtrlDown
	KeyCtrlRight
	KeyCtrlLeft
	KeyCtrlHome
	KeyCtrlEnd
	KeyShiftUp
	KeyShiftDown
	KeyShiftRight
	KeyShiftLeft
	KeyShiftHome
	KeyShiftEnd
	KeyCtrlShiftUp
	KeyCtrlShiftDown
	KeyCtrlShiftLeft
	KeyCtrlShiftRight
	KeyCtrlShiftHome
	KeyCtrlShiftEnd
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
)

// Mappings for control keys and other special keys to friendly consts.
var keyNames = map[KeyType]string{
	// Control keys.
	keyNUL: "ctrl+@", // also ctrl+` (that's ctrl+backtick)
	keySOH: "ctrl+a",
	keySTX: "ctrl+b",
	keyETX: "ctrl+c",
	keyEOT: "ctrl+d",
	keyENQ: "ctrl+e",
	keyACK: "ctrl+f",
	keyBEL: "ctrl+g",
	keyBS:  "ctrl+h",
	keyHT:  "tab", // also ctrl+i
	keyLF:  "ctrl+j",
	keyVT:  "ctrl+k",
	keyFF:  "ctrl+l",
	keyCR:  "enter",
	keySO:  "ctrl+n",
	keySI:  "ctrl+o",
	keyDLE: "ctrl+p",
	keyDC1: "ctrl+q",
	keyDC2: "ctrl+r",
	keyDC3: "ctrl+s",
	keyDC4: "ctrl+t",
	keyNAK: "ctrl+u",
	keySYN: "ctrl+v",
	keyETB: "ctrl+w",
	keyCAN: "ctrl+x",
	keyEM:  "ctrl+y",
	keySUB: "ctrl+z",
	keyESC: "esc",
	keyFS:  "ctrl+\\",
	keyGS:  "ctrl+]",
	keyRS:  "ctrl+^",
	keyUS:  "ctrl+_",
	keyDEL: "backspace",

	// Other keys.
	KeyRunes:          "runes",
	KeyUp:             "up",
	KeyDown:           "down",
	KeyRight:          "right",
	KeySpace:          " ", // for backwards compatibility
	KeyLeft:           "left",
	KeyShiftTab:       "shift+tab",
	KeyHome:           "home",
	KeyEnd:            "end",
	KeyCtrlHome:       "ctrl+home",
	KeyCtrlEnd:        "ctrl+end",
	KeyShiftHome:      "shift+home",
	KeyShiftEnd:       "shift+end",
	KeyCtrlShiftHome:  "ctrl+shift+home",
	KeyCtrlShiftEnd:   "ctrl+shift+end",
	KeyPgUp:           "pgup",
	KeyPgDown:         "pgdown",
	KeyCtrlPgUp:       "ctrl+pgup",
	KeyCtrlPgDown:     "ctrl+pgdown",
	KeyDelete:         "delete",
	KeyInsert:         "insert",
	KeyCtrlUp:         "ctrl+up",
	KeyCtrlDown:       "ctrl+down",
	KeyCtrlRight:      "ctrl+right",
	KeyCtrlLeft:       "ctrl+left",
	KeyShiftUp:        "shift+up",
	KeyShiftDown:      "shift+down",
	KeyShiftRight:     "shift+right",
	KeyShiftLeft:      "shift+left",
	KeyCtrlShiftUp:    "ctrl+shift+up",
	KeyCtrlShiftDown:  "ctrl+shift+down",
	KeyCtrlShiftLeft:  "ctrl+shift+left",
	KeyCtrlShiftRight: "ctrl+shift+right",
	KeyF1:             "f1",
	KeyF2:             "f2",
	KeyF3:             "f3",
	KeyF4:             "f4",
	KeyF5:             "f5",
	KeyF6:             "f6",
	KeyF7:             "f7",
	KeyF8:             "f8",
	KeyF9:             "f9",
	KeyF10:            "f10",
	KeyF11:            "f11",
	KeyF12:            "f12",
	KeyF13:            "f13",
	KeyF14:            "f14",
	KeyF15:            "f15",
	KeyF16:            "f16",
	KeyF17:            "f17",
	KeyF18:            "f18",
	KeyF19:            "f19",
	KeyF20:            "f20",
}

// Sequence mappings.
// FromTea converts a Bubble Tea v2 key or paste message into the internal
// key model. Non-key messages return ok=false.
func FromTea(message tea.Msg) (KeyMsg, bool) {
	switch msg := message.(type) {
	case KeyMsg:
		return msg, true
	case tea.PasteMsg:
		return KeyMsg{
			Type:     KeyRunes,
			Runes:    []rune(msg.Content),
			Text:     msg.Content,
			Paste:    true,
			Original: message,
		}, true
	case tea.KeyPressMsg:
		key := tea.Key(msg)
		out := KeyMsg{
			Mod:      key.Mod,
			Code:     key.Code,
			Text:     key.Text,
			Alt:      key.Mod.Contains(tea.ModAlt),
			Super:    key.Mod.Contains(tea.ModSuper),
			Original: message,
		}
		out.Type = keyType(key)
		if out.Type == KeyRunes {
			if key.Text != "" {
				out.Runes = []rune(key.Text)
			} else if key.Code > 0 {
				out.Runes = []rune{key.Code}
			}
		}
		return out, true
	}
	return KeyMsg{}, false
}

func keyType(k tea.Key) KeyType {
	ctrl := k.Mod.Contains(tea.ModCtrl)
	shift := k.Mod.Contains(tea.ModShift)
	switch k.Code {
	case tea.KeyEnter:
		if ctrl {
			return KeyCtrlM
		}
		return KeyEnter
	case tea.KeyTab:
		if shift {
			return KeyShiftTab
		}
		if ctrl {
			return KeyCtrlI
		}
		return KeyTab
	case tea.KeyEscape:
		if ctrl {
			return KeyCtrlOpenBracket
		}
		return KeyEsc
	case tea.KeyBackspace:
		if ctrl {
			return KeyCtrlH
		}
		return KeyBackspace
	case tea.KeyDelete:
		return KeyDelete
	case tea.KeyInsert:
		return KeyInsert
	case tea.KeySpace:
		if ctrl {
			return KeyCtrlAt
		}
		return KeySpace
	case tea.KeyUp:
		return arrowKey(KeyUp, KeyCtrlUp, KeyShiftUp, KeyCtrlShiftUp, ctrl, shift)
	case tea.KeyDown:
		return arrowKey(KeyDown, KeyCtrlDown, KeyShiftDown, KeyCtrlShiftDown, ctrl, shift)
	case tea.KeyLeft:
		return arrowKey(KeyLeft, KeyCtrlLeft, KeyShiftLeft, KeyCtrlShiftLeft, ctrl, shift)
	case tea.KeyRight:
		return arrowKey(KeyRight, KeyCtrlRight, KeyShiftRight, KeyCtrlShiftRight, ctrl, shift)
	case tea.KeyHome:
		return arrowKey(KeyHome, KeyCtrlHome, KeyShiftHome, KeyCtrlShiftHome, ctrl, shift)
	case tea.KeyEnd:
		return arrowKey(KeyEnd, KeyCtrlEnd, KeyShiftEnd, KeyCtrlShiftEnd, ctrl, shift)
	case tea.KeyPgUp:
		if ctrl {
			return KeyCtrlPgUp
		}
		return KeyPgUp
	case tea.KeyPgDown:
		if ctrl {
			return KeyCtrlPgDown
		}
		return KeyPgDown
	case tea.KeyF1:
		return KeyF1
	case tea.KeyF2:
		return KeyF2
	case tea.KeyF3:
		return KeyF3
	case tea.KeyF4:
		return KeyF4
	case tea.KeyF5:
		return KeyF5
	case tea.KeyF6:
		return KeyF6
	case tea.KeyF7:
		return KeyF7
	case tea.KeyF8:
		return KeyF8
	case tea.KeyF9:
		return KeyF9
	case tea.KeyF10:
		return KeyF10
	case tea.KeyF11:
		return KeyF11
	case tea.KeyF12:
		return KeyF12
	case tea.KeyF13:
		return KeyF13
	case tea.KeyF14:
		return KeyF14
	case tea.KeyF15:
		return KeyF15
	case tea.KeyF16:
		return KeyF16
	case tea.KeyF17:
		return KeyF17
	case tea.KeyF18:
		return KeyF18
	case tea.KeyF19:
		return KeyF19
	case tea.KeyF20:
		return KeyF20
	}
	if ctrl {
		switch {
		case k.Code >= 'a' && k.Code <= 'z':
			return KeyType(k.Code-'a') + KeyCtrlA
		case k.Code >= 'A' && k.Code <= 'Z':
			return KeyType(k.Code-'A') + KeyCtrlA
		case k.Code == '@' || k.Code == 0:
			return KeyCtrlAt
		case k.Code == '[':
			return KeyCtrlOpenBracket
		case k.Code == '\\':
			return KeyCtrlBackslash
		case k.Code == ']':
			return KeyCtrlCloseBracket
		case k.Code == '^':
			return KeyCtrlCaret
		case k.Code == '_':
			return KeyCtrlUnderscore
		case k.Code == '?':
			return KeyCtrlQuestionMark
		}
	}
	if k.Text != "" || k.Code > 0 {
		return KeyRunes
	}
	return KeyNull
}

func arrowKey(plain, ctrl, shift, ctrlShift KeyType, hasCtrl, hasShift bool) KeyType {
	switch {
	case hasCtrl && hasShift:
		return ctrlShift
	case hasCtrl:
		return ctrl
	case hasShift:
		return shift
	default:
		return plain
	}
}

// OriginalMessage returns the v2 message Bubbles components should receive.
// Tests and internal callers may construct KeyMsg directly; in that case the
// key is converted back to a v2 KeyPressMsg.
func OriginalMessage(message tea.Msg) tea.Msg {
	key, ok := message.(KeyMsg)
	if !ok {
		return message
	}
	if key.Original != nil {
		return key.Original
	}
	return ToTea(key)
}

// ToTea converts an internally constructed key back to a v2 key press.
// Bubble Tea v2 encodes modified keys as the unshifted code plus Mod, while the
// internal model keeps control keys as their own KeyType (ctrl+u is KeyCtrlU),
// so the conversion restores the modifier bits the Bubbles components match on.
func ToTea(message KeyMsg) tea.KeyPressMsg {
	key := Key(message)
	code := tea.KeyEscape
	var text string
	if len(key.Runes) > 0 {
		text = string(key.Runes)
	}
	mod := key.Mod
	arrows := func(plain rune, hasCtrl, hasShift bool) {
		code = plain
		switch {
		case hasCtrl && hasShift:
			mod |= tea.ModCtrl | tea.ModShift
		case hasCtrl:
			mod |= tea.ModCtrl
		case hasShift:
			mod |= tea.ModShift
		}
	}
	switch key.Type {
	case KeyRunes:
		if len(key.Runes) > 0 {
			code = key.Runes[0]
		}
	case KeyEnter:
		code = tea.KeyEnter
		text = ""
	case KeyTab:
		code = tea.KeyTab
		text = ""
	case KeyShiftTab:
		code = tea.KeyTab
		mod |= tea.ModShift
		text = ""
	case KeyEsc:
		code = tea.KeyEscape
		text = ""
	case KeyBackspace, KeyCtrlH:
		code = tea.KeyBackspace
		text = ""
	case KeyDelete:
		code = tea.KeyDelete
		text = ""
	case KeyInsert:
		code = tea.KeyInsert
		text = ""
	case KeySpace:
		code = tea.KeySpace
		text = " "
	case KeyCtrlAt:
		code = tea.KeySpace
		mod |= tea.ModCtrl
		text = " "
	case KeyUp, KeyCtrlUp, KeyShiftUp, KeyCtrlShiftUp:
		arrows(tea.KeyUp,
			key.Type == KeyCtrlUp || key.Type == KeyCtrlShiftUp,
			key.Type == KeyShiftUp || key.Type == KeyCtrlShiftUp)
	case KeyDown, KeyCtrlDown, KeyShiftDown, KeyCtrlShiftDown:
		arrows(tea.KeyDown,
			key.Type == KeyCtrlDown || key.Type == KeyCtrlShiftDown,
			key.Type == KeyShiftDown || key.Type == KeyCtrlShiftDown)
	case KeyLeft, KeyCtrlLeft, KeyShiftLeft, KeyCtrlShiftLeft:
		arrows(tea.KeyLeft,
			key.Type == KeyCtrlLeft || key.Type == KeyCtrlShiftLeft,
			key.Type == KeyShiftLeft || key.Type == KeyCtrlShiftLeft)
	case KeyRight, KeyCtrlRight, KeyShiftRight, KeyCtrlShiftRight:
		arrows(tea.KeyRight,
			key.Type == KeyCtrlRight || key.Type == KeyCtrlShiftRight,
			key.Type == KeyShiftRight || key.Type == KeyCtrlShiftRight)
	case KeyHome, KeyCtrlHome, KeyShiftHome, KeyCtrlShiftHome:
		arrows(tea.KeyHome,
			key.Type == KeyCtrlHome || key.Type == KeyCtrlShiftHome,
			key.Type == KeyShiftHome || key.Type == KeyCtrlShiftHome)
	case KeyEnd, KeyCtrlEnd, KeyShiftEnd, KeyCtrlShiftEnd:
		arrows(tea.KeyEnd,
			key.Type == KeyCtrlEnd || key.Type == KeyCtrlShiftEnd,
			key.Type == KeyShiftEnd || key.Type == KeyCtrlShiftEnd)
	case KeyPgUp:
		code = tea.KeyPgUp
	case KeyCtrlPgUp:
		code = tea.KeyPgUp
		mod |= tea.ModCtrl
	case KeyPgDown:
		code = tea.KeyPgDown
	case KeyCtrlPgDown:
		code = tea.KeyPgDown
		mod |= tea.ModCtrl
	case KeyF1:
		code = tea.KeyF1
	case KeyF2:
		code = tea.KeyF2
	case KeyF3:
		code = tea.KeyF3
	case KeyF4:
		code = tea.KeyF4
	case KeyF5:
		code = tea.KeyF5
	case KeyF6:
		code = tea.KeyF6
	case KeyF7:
		code = tea.KeyF7
	case KeyF8:
		code = tea.KeyF8
	case KeyF9:
		code = tea.KeyF9
	case KeyF10:
		code = tea.KeyF10
	case KeyF11:
		code = tea.KeyF11
	case KeyF12:
		code = tea.KeyF12
	default:
		if key.Type >= KeyCtrlA && key.Type <= KeyCtrlZ {
			// Control keys: v2 sends the unshifted letter with ModCtrl.
			code = rune('a' + (key.Type - KeyCtrlA))
			mod |= tea.ModCtrl
			text = ""
		}
	}
	if key.Alt {
		mod |= tea.ModAlt
	}
	if key.Super {
		mod |= tea.ModSuper
	}
	return tea.KeyPressMsg{Code: code, Text: text, Mod: mod}
}
