package tea

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	pets "codex_go/tui/pets"
)

func TestModelPetsSelectionLoadsAndRendersAmbientPet(t *testing.T) {
	codexHome := t.TempDir()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:     90,
		Height:    24,
		CodexHome: codexHome,
		PetEnv:    map[string]string{"KITTY_WINDOW_ID": "1"},
		PetFetch: func(url string, maxBytes int64) ([]byte, error) {
			return petTestSpritesheet(t), nil
		},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			result := SettingsWriteResult{FilePath: `D:\codex\config.toml`}
			for _, edit := range edits {
				if edit.KeyPath == "tui.pet" {
					result.TUIPet, _ = edit.Value.(string)
				}
			}
			return result, nil
		},
	})

	typeText(t, model, "/pets dewey")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	if model.tuiPet != "dewey" {
		t.Fatalf("tuiPet = %q", model.tuiPet)
	}
	if model.petRuntime == nil || !model.petRuntime.installed() {
		t.Fatal("ambient pet should be installed after selection load")
	}

	var output bytes.Buffer
	model.petRuntime.setOutput(&output)
	model.View()
	wrapper := &petOutputWriter{runtime: model.petRuntime}
	if _, err := wrapper.Write([]byte("frame")); err != nil {
		t.Fatalf("write: %v", err)
	}
	rendered := output.String()
	if !strings.Contains(rendered, "\x1b_Ga=T,t=d") {
		t.Fatalf("kitty transmit payload missing:\n%q", rendered)
	}
	if !strings.Contains(rendered, "\x1b7") || !strings.Contains(rendered, "\x1b8") {
		t.Fatalf("saved-cursor wrapper missing:\n%q", rendered)
	}
}

func TestPetOutputWriterPreservesTerminalFileDescriptor(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "pet-output-*.txt")
	if err != nil {
		t.Fatalf("create output file: %v", err)
	}
	defer file.Close()

	runtime := newPetRuntime(file, nil)
	writer := &petOutputWriter{runtime: runtime}
	if got := writer.Fd(); got != file.Fd() {
		t.Fatalf("Fd() = %d, want %d", got, file.Fd())
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if _, err := writer.Write([]byte("frame")); err != nil {
		t.Fatalf("Write() after Close(): %v", err)
	}

	bufferWriter := &petOutputWriter{runtime: newPetRuntime(&bytes.Buffer{}, nil)}
	if got := bufferWriter.Fd(); got != ^uintptr(0) {
		t.Fatalf("non-file Fd() = %d, want invalid descriptor", got)
	}
}

func TestModelPickerSelectionDoesNotAccumulateComposerRows(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-b"})
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-a", Label: "GPT A", IsDefault: true},
			{ID: "gpt-b", Label: "GPT B"},
		},
	})
	model.openModelPicker()

	var output bytes.Buffer
	input, inputWriter := io.Pipe()
	defer input.Close()
	defer inputWriter.Close()
	model.petRuntime.setOutput(&output)
	program := bubbletea.NewProgram(
		model,
		bubbletea.WithContext(context.Background()),
		bubbletea.WithInput(input),
		bubbletea.WithOutput(&petOutputWriter{runtime: model.petRuntime}),
		bubbletea.WithReportFocus(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	program.Send(bubbletea.WindowSizeMsg{Width: 90, Height: 24})
	time.Sleep(20 * time.Millisecond)
	program.Send(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'1'}})
	time.Sleep(400 * time.Millisecond)
	program.Quit()
	_ = inputWriter.Close()
	if err := <-done; err != nil {
		t.Fatalf("run program: %v", err)
	}

	terminal := newTestVirtualTerminal(90, 24)
	terminal.WriteString(output.String())
	if count := strings.Count(output.String(), "Ask Codex"); count != 1 {
		t.Fatalf("model selection wrote the composer %d times, want exactly 1", count)
	}
	if count := strings.Count(terminal.Snapshot(), "Ask Codex"); count != 1 {
		t.Fatalf("terminal contains %d composer rows, want exactly 1\n%s", count, terminal.Snapshot())
	}
}

func TestModelPetsDisableClearsAmbientPetImage(t *testing.T) {
	codexHome := t.TempDir()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:     90,
		Height:    24,
		CodexHome: codexHome,
		PetEnv:    map[string]string{"KITTY_WINDOW_ID": "1"},
		PetFetch: func(url string, maxBytes int64) ([]byte, error) {
			return petTestSpritesheet(t), nil
		},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			result := SettingsWriteResult{FilePath: `D:\codex\config.toml`}
			for _, edit := range edits {
				if edit.KeyPath == "tui.pet" {
					result.TUIPet, _ = edit.Value.(string)
				}
			}
			return result, nil
		},
	})

	typeText(t, model, "/pets dewey")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !model.petRuntime.installed() {
		t.Fatal("pet should be installed")
	}
	model.View()

	var output bytes.Buffer
	model.petRuntime.setOutput(&output)
	wrapper := &petOutputWriter{runtime: model.petRuntime}
	wrapper.Write([]byte("frame"))
	if !strings.Contains(output.String(), "\x1b_Ga=T") {
		t.Fatalf("expected pet draw, got %q", output.String())
	}

	typeText(t, model, "/pets off")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.petRuntime.installed() {
		t.Fatal("pet should be disabled")
	}
	output.Reset()
	wrapper.Write([]byte("frame"))
	if !strings.Contains(output.String(), "\x1b_Ga=d") {
		t.Fatalf("kitty delete payload missing after disable:\n%q", output.String())
	}
	if strings.Contains(output.String(), "\x1b_Ga=T") {
		t.Fatalf("pet should not redraw after disable:\n%q", output.String())
	}
}

func TestModelPetTickAdvancesAnimationFrame(t *testing.T) {
	codexHome := t.TempDir()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:     90,
		Height:    24,
		CodexHome: codexHome,
		PetEnv:    map[string]string{"KITTY_WINDOW_ID": "1"},
		PetFetch: func(url string, maxBytes int64) ([]byte, error) {
			return petTestSpritesheet(t), nil
		},
	})
	// Install directly to avoid needing a settings write.
	petState, err := pets.LoadAmbientPet("dewey", codexHome, true, map[string]string{"KITTY_WINDOW_ID": "1"}, model.petFetch)
	if err != nil {
		t.Fatalf("load pet: %v", err)
	}
	model.petRuntime.install(petState)
	var output bytes.Buffer
	model.petRuntime.setOutput(&output)

	cmd := model.petTickCmd()
	if cmd == nil {
		t.Fatal("expected a pet tick command")
	}
	updated, nextCmd := model.Update(petTickMsg{})
	if nextCmd == nil {
		t.Fatal("expected tick to schedule a repaint and the next tick")
	}
	if output.Len() != 0 {
		t.Fatalf("tick must not write the pet payload directly:\n%q", output.String())
	}
	if _, ok := updated.(*Model); !ok {
		t.Fatalf("Update returned %T", updated)
	}

	// Animation frames are written directly through the synchronized output
	// writer, so Bubble Tea never traverses or repaints the inline TUI.
	draw := model.petDrawCmd()
	if draw == nil {
		t.Fatal("expected a pet draw command while the surface is inactive")
	}
	if msg := draw(); msg != nil {
		t.Fatalf("pet draw command returned %T, want nil", msg)
	}
	if !strings.Contains(output.String(), "\x1b_Ga=T") {
		t.Fatalf("direct draw should emit pet frame:\n%q", output.String())
	}
}

func TestModelPetAnimationDoesNotAccumulateComposerRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "kitty", env: map[string]string{"KITTY_WINDOW_ID": "1"}},
		{name: "sixel", env: map[string]string{"WT_SESSION": "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codexHome := t.TempDir()
			state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
			var output bytes.Buffer
			input, inputWriter := io.Pipe()
			defer input.Close()
			defer inputWriter.Close()
			model := NewModel(state, Options{
				Width:     90,
				Height:    24,
				CodexHome: codexHome,
				PetEnv:    tc.env,
				PetFetch: func(url string, maxBytes int64) ([]byte, error) {
					return petTestSpritesheet(t), nil
				},
				OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
					return SettingsWriteResult{FilePath: `D:\codex\config.toml`, TUIPet: "dewey"}, nil
				},
			})
			runTeaCmd(t, model, model.setTUIPet("dewey"))
			if !model.petRuntime.installed() {
				t.Fatal("pet did not install before starting the program")
			}
			model.petRuntime.mu.Lock()
			model.petRuntime.ambient.SetNotification(pets.PetNotificationRunning, "Thinking", time.Now())
			model.petRuntime.mu.Unlock()
			model.openThemePicker()
			model.petRuntime.setOutput(&output)
			program := bubbletea.NewProgram(
				model,
				bubbletea.WithContext(context.Background()),
				bubbletea.WithInput(input),
				bubbletea.WithOutput(&petOutputWriter{runtime: model.petRuntime}),
				bubbletea.WithReportFocus(),
			)

			done := make(chan error, 1)
			go func() {
				_, err := program.Run()
				done <- err
			}()
			time.Sleep(20 * time.Millisecond)
			program.Send(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
			program.Send(petTickMsg{})
			time.Sleep(500 * time.Millisecond)
			program.Quit()
			_ = inputWriter.Close()
			if err := <-done; err != nil {
				t.Fatalf("run program: %v", err)
			}

			terminal := newTestVirtualTerminal(90, 24)
			terminal.WriteString(output.String())
			snapshot := terminal.Snapshot()
			if count := strings.Count(output.String(), "Ask Codex"); count != 1 {
				t.Fatalf("animation wrote the composer %d times, want exactly 1", count)
			}
			if count := strings.Count(output.String(), "\x1b7"); count < 2 {
				t.Fatalf("pet image frames = %d, want at least 2", count)
			}
			if count := strings.Count(snapshot, "Ask Codex"); count != 1 {
				t.Fatalf("composer placeholder count = %d, want 1\n--- terminal ---\n%s\n--- raw output ---\n%q", count, snapshot, output.String())
			}
		})
	}
}

func TestModelPetDrawSuppressedWhileSurfaceActive(t *testing.T) {
	codexHome := t.TempDir()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:     90,
		Height:    24,
		CodexHome: codexHome,
		PetEnv:    map[string]string{"KITTY_WINDOW_ID": "1"},
		PetFetch: func(url string, maxBytes int64) ([]byte, error) {
			return petTestSpritesheet(t), nil
		},
	})
	petState, err := pets.LoadAmbientPet("dewey", codexHome, true, map[string]string{"KITTY_WINDOW_ID": "1"}, model.petFetch)
	if err != nil {
		t.Fatalf("load pet: %v", err)
	}
	model.petRuntime.install(petState)

	if model.petDrawCmd() == nil {
		t.Fatal("expected draw while surface is inactive")
	}
	model.slashPopup.Active = true
	if model.petDrawCmd() != nil {
		t.Fatal("draw must be suppressed while a popup covers the surface")
	}
}

func TestModelPetsUnsupportedTerminalShowsMessage(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{Width: 90, Height: 24})
	typeText(t, model, "/pets dewey")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		t.Fatalf("unsupported terminal should not select pet, got cmd %#v", cmd)
	}
	if model.tuiPet != "" {
		t.Fatalf("tuiPet should stay empty, got %q", model.tuiPet)
	}
	if !strings.Contains(model.View(), "Pets aren't available in this terminal") {
		t.Fatalf("missing unsupported-terminal notice:\n%s", model.View())
	}
}

func petTestSpritesheet(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, pets.SpritesheetWidth, pets.SpritesheetHeight))
	for y := 0; y < pets.SpritesheetHeight; y++ {
		for x := 0; x < pets.SpritesheetWidth; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode spritesheet: %v", err)
	}
	return buffer.Bytes()
}
