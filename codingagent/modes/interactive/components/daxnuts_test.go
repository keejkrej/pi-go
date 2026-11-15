package components

import "testing"

func TestPlayDaxnuts3dNilSafe(t *testing.T) {
	if PlayDaxnuts3d(nil) {
		t.Fatalf("expected PlayDaxnuts3d(nil) to return false without panic")
	}
}
