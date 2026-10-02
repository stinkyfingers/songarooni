//go:build linux

package audio

import "testing"

func TestParseArecordDeviceList(t *testing.T) {
	const output = `**** List of CAPTURE Hardware Devices ****
card 0: Headphones [bcm2835 Headphones], device 0: bcm2835 Headphones [bcm2835 Headphones]
  Subdevices: 4/4
  Subdevice #0: subdevice #0
card 2: CODEC [USB Audio CODEC], device 0: USB Audio [USB Audio]
  Subdevices: 1/1
  Subdevice #0: subdevice #0
`

	got := parseArecordDeviceList(output)

	want := []DeviceInfo{
		{Card: 0, Device: 0, name: "bcm2835 Headphones: bcm2835 Headphones"},
		{Card: 2, Device: 0, name: "USB Audio CODEC: USB Audio"},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d devices, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("device %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseArecordDeviceList_NoSoundcards(t *testing.T) {
	got := parseArecordDeviceList("arecord: device_list:274: no soundcards found...\n")
	if len(got) != 0 {
		t.Fatalf("got %d devices, want 0: %+v", len(got), got)
	}
}
