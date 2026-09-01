package video

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

// 结构单测与真实可解码夹具分开：电影1000Hz、轨道12288Hz和512tick采样必须得24fps。
func TestVideoG6MediaClockMatrix(t *testing.T) {
	clock := func(version byte, scale uint32, duration uint64) []byte {
		if version == 1 {
			raw := make([]byte, 32)
			raw[0] = 1
			binary.BigEndian.PutUint32(raw[20:24], scale)
			binary.BigEndian.PutUint64(raw[24:32], duration)
			return raw
		}
		raw := make([]byte, 24)
		raw[0] = version
		binary.BigEndian.PutUint32(raw[12:16], scale)
		binary.BigEndian.PutUint32(raw[16:20], uint32(duration))
		return raw
	}
	for _, tc := range []struct {
		name                       string
		movieVersion, mediaVersion byte
		bad                        string
		pass                       bool
	}{
		{"不同时间基准", 0, 0, "", true}, {"两个64位时钟", 1, 1, "", true}, {"未知电影版本", 2, 0, "", false}, {"未知媒体版本", 0, 2, "", false}, {"缺失媒体时钟", 0, 0, "missing", false}, {"零媒体时基", 0, 0, "zero", false}, {"截断媒体时钟", 0, 1, "truncated", false}, {"零采样数", 0, 0, "count", false}, {"变帧率", 0, 0, "vfr", false}, {"非整数帧率", 0, 0, "fractional", false}, {"截断采样表", 0, 0, "table", false},
		{"零媒体时长", 0, 0, "zero_duration", false}, {"采样总时长错配", 0, 0, "short_samples", false}, {"首个视频缺codec仍有第二轨", 0, 0, "empty_first", false},
		{"64位时长高位非零", 1, 1, "wide_media", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mvhd := clock(tc.movieVersion, 1000, 5000)
			mdhd := clock(tc.mediaVersion, 12288, 61440)
			if tc.bad == "zero" {
				mdhd = clock(0, 0, 61440)
			}
			if tc.bad == "truncated" {
				mdhd = mdhd[:20]
			}
			if tc.bad == "zero_duration" {
				mdhd = clock(0, 12288, 0)
			}
			if tc.bad == "wide_media" {
				mvhd = clock(1, 1200000000, 6000000000)
				mdhd = clock(1, 1200000000, 6000000000)
			}
			tkhd := make([]byte, 84)
			binary.BigEndian.PutUint32(tkhd[76:80], 1280<<16)
			binary.BigEndian.PutUint32(tkhd[80:84], 720<<16)
			hdlr := make([]byte, 12)
			copy(hdlr[8:], "vide")
			stsd := make([]byte, 16)
			binary.BigEndian.PutUint32(stsd[4:8], 1)
			binary.BigEndian.PutUint32(stsd[8:12], 8)
			copy(stsd[12:], "avc1")
			stts := make([]byte, 24)
			binary.BigEndian.PutUint32(stts[4:8], 2)
			binary.BigEndian.PutUint32(stts[8:12], 30)
			binary.BigEndian.PutUint32(stts[12:16], 512)
			binary.BigEndian.PutUint32(stts[16:20], 90)
			binary.BigEndian.PutUint32(stts[20:24], 512)
			switch tc.bad {
			case "wide_media":
				binary.BigEndian.PutUint32(stts[12:16], 50000000)
				binary.BigEndian.PutUint32(stts[20:24], 50000000)
			case "short_samples":
				binary.BigEndian.PutUint32(stts[8:12], 1)
			case "count":
				binary.BigEndian.PutUint32(stts[8:12], 0)
			case "vfr":
				binary.BigEndian.PutUint32(stts[20:24], 256)
			case "fractional":
				mdhd = clock(0, 12288, 61560)
				binary.BigEndian.PutUint32(stts[12:16], 513)
				binary.BigEndian.PutUint32(stts[20:24], 513)
			case "table":
				stts = stts[:23]
			}
			media := makeBox("hdlr", hdlr)
			if tc.bad != "missing" {
				media = append(media, makeBox("mdhd", mdhd)...)
			}
			media = append(media, makeBox("minf", makeBox("stbl", append(makeBox("stsd", stsd), makeBox("stts", stts)...)))...)
			moov := append(makeBox("mvhd", mvhd), makeBox("trak", append(makeBox("tkhd", tkhd), makeBox("mdia", media)...))...)
			if tc.bad == "empty_first" {
				badMedia := bytes.Replace(append([]byte(nil), media...), []byte("stsd"), []byte("free"), 1)
				first := makeBox("trak", append(makeBox("tkhd", tkhd), makeBox("mdia", badMedia)...))
				moov = append(append(makeBox("mvhd", mvhd), first...), makeBox("trak", append(makeBox("tkhd", tkhd), makeBox("mdia", media)...))...)
			}
			raw := append(makeBox("ftyp", []byte("isom0000")), makeBox("moov", moov)...)
			raw = append(raw, makeBox("mdat", []byte("test-media"))...)
			got, err := NewVideoMediaProbe(defaultVideoProbeLimits()).Probe(context.Background(), StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-clock", ContentID: "content-clock", MediaType: "video/mp4"}, ReaderAt: bytes.NewReader(raw), SizeBytes: int64(len(raw)), RangeMode: "supported"})
			if tc.pass {
				if err != nil || got.FrameRate != 24 || got.DurationMillis != 5000 {
					t.Fatalf("时基解析错误：fps=%d duration=%d err=%v", got.FrameRate, got.DurationMillis, err)
				}
			} else if err == nil {
				t.Fatal("无效媒体时基或采样表必须拒绝")
			}
			if tc.bad == "fractional" && !errors.Is(err, ErrMediaResourceLimit) {
				t.Fatalf("时长一致的非整数帧率必须命中帧率拒绝，实际%v", err)
			}
		})
	}
}
