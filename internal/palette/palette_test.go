package palette

import (
	"image/color"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"
)

func TestValueParsesHexAsOpaqueRGBA(t *testing.T) {
	got, ok := Color("#6b50ff").Value()

	if !ok {
		t.Fatal("ok = false, want true")
	}
	if want := (color.RGBA{R: 0x6b, G: 0x50, B: 0xff, A: 0xff}); got != want {
		t.Errorf("Value() = %v, want %v", got, want)
	}
}

func TestValueMapsLowNumbersToBasicColors(t *testing.T) {
	got, ok := Color("9").Value()

	if !ok {
		t.Fatal("ok = false, want true")
	}
	if want := xansi.BasicColor(9); got != want {
		t.Errorf("Value() = %v, want %v", got, want)
	}
}

func TestValueMapsHighNumbersToIndexedColors(t *testing.T) {
	for _, in := range []string{"16", "255"} {
		got, ok := Color(in).Value()

		if !ok {
			t.Fatalf("%q: ok = false, want true", in)
		}
		n := map[string]int{"16": 16, "255": 255}[in]
		if want := xansi.IndexedColor(n); got != want {
			t.Errorf("%q: Value() = %v, want %v", in, got, want)
		}
	}
}

func TestValueRejectsDefaultAndInvalidColors(t *testing.T) {
	invalid := []Color{Default, "-1", "256", "red", "#fff", "#1234567", "#gggggg", "#"}

	for _, c := range invalid {
		if got, ok := c.Value(); ok || got != nil {
			t.Errorf("Color(%q).Value() = (%v, %v), want (nil, false)", string(c), got, ok)
		}
	}
}

func TestPaletteRolesAreAllValidColors(t *testing.T) {
	roles := map[string]Color{
		"Brand": Brand, "Accent": Accent, "OnBrand": OnBrand, "OnBright": OnBright,
		"Muted": Muted, "Rule": Rule, "Key": Key, "Info": Info, "OK": OK, "Warn": Warn,
		"Fail": Fail, "Claude": Claude, "Tool": Tool, "Live": Live, "Cron": Cron,
		"Branch": Branch, "Surface": Surface,
	}

	for name, c := range roles {
		if _, ok := c.Value(); !ok {
			t.Errorf("%s = %q is not a valid color", name, string(c))
		}
	}
}
