package main

import (
	"github.com/dop251/goja"
	"testing"
)

func TestBrowserRepairOnlySeeksRequestedMainVideo(t *testing.T) {
	for _, test := range []struct {
		name, source string
		ad           bool
		duration     float64
		want         bool
	}{
		{"requested main", "requestedID", false, 20, true},
		{"preroll", "requestedID", true, 20, false},
		{"different video", "anotherID", false, 20, false},
		{"different duration", "requestedID", false, 10, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			vm := goja.New()
			_ = vm.Set("source", test.source)
			_ = vm.Set("ad", test.ad)
			_ = vm.Set("duration", test.duration)
			_, err := vm.RunString(`let seeks=0; const video={duration,currentTime:12};const player={classList:{contains:()=>ad},getVideoData:()=>({video_id:source}),seekTo:(position)=>{seeks++;video.currentTime=position;},playVideo:()=>{}};const document={querySelector:()=>video,getElementById:()=>player};`)
			if err != nil {
				t.Fatal(err)
			}
			value, err := vm.RunString(browserRepairPlaybackScript(3500, 20000, "requestedID"))
			if err != nil || value.ToBoolean() != test.want {
				t.Fatalf("seek accepted=%v error=%v", value, err)
			}
			position, _ := vm.RunString("video.currentTime")
			if test.want && position.ToFloat() != 3.5 {
				t.Fatalf("seek exceeded verified repair position: %v", position)
			}
			if !test.want {
				count, _ := vm.RunString("seeks")
				if count.ToInteger() != 0 {
					t.Fatal("sought inside an ad or another source")
				}
			}
		})
	}
}

func TestBrowserRepairReloadsOnlyRequestedVideoAtVerifiedPosition(t *testing.T) {
	vm := goja.New()
	_, err := vm.RunString(`let loaded;const video={duration:20};const player={classList:{contains:()=>false},getVideoData:()=>({video_id:'requestedID'}),loadVideoById:(value)=>{loaded=value;}};const document={querySelector:()=>video,getElementById:()=>player};`)
	if err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString(browserRepairPlaybackScript(3500, 20000, "requestedID", true))
	if err != nil || !value.ToBoolean() {
		t.Fatal(err)
	}
	if ok, err := vm.RunString("loaded.videoId==='requestedID' && loaded.startSeconds===3.5"); err != nil || !ok.ToBoolean() {
		t.Fatalf("wrong reload %v %v", ok, err)
	}
}
