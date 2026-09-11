package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGetFileResolvesAPath(t *testing.T) {
	fake := newFakeAPI(t)
	fake.reply["getFile"] = map[string]any{"file_path": "voice/file_1.oga", "file_size": 4096}

	file, err := fake.client().GetFile(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if file.FilePath != "voice/file_1.oga" {
		t.Errorf("file path = %q", file.FilePath)
	}
	if file.FileSize != 4096 {
		t.Errorf("file size = %d", file.FileSize)
	}

	calls := fake.callsTo("getFile")
	if len(calls) != 1 {
		t.Fatalf("getFile called %d times", len(calls))
	}
	if calls[0].body["file_id"] != "abc123" {
		t.Errorf("file id = %v", calls[0].body["file_id"])
	}
}

func TestGetFileRefusesAnAnswerWithoutAPath(t *testing.T) {
	fake := newFakeAPI(t)
	fake.reply["getFile"] = map[string]any{"file_size": 4096}

	if _, err := fake.client().GetFile(context.Background(), "abc123"); err == nil {
		t.Error("expected an error when getFile answers with no path")
	}
}

func TestDownloadFetchesTheBytes(t *testing.T) {
	fake := newFakeAPI(t)
	fake.fileContent = []byte("OggS a recording")

	got, err := fake.client().Download(context.Background(), "voice/file_1.oga", 1<<20)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != "OggS a recording" {
		t.Errorf("content = %q", got)
	}

	calls := fake.callsTo("download")
	if len(calls) != 1 {
		t.Fatalf("download called %d times", len(calls))
	}
	// The bytes live under /file/bot<token>/, not under /bot<token>/.
	if !strings.HasPrefix(calls[0].path, "/file/bot") {
		t.Errorf("download went to %q, want the file endpoint", calls[0].path)
	}
	if !strings.HasSuffix(calls[0].path, "voice/file_1.oga") {
		t.Errorf("download went to %q, want the file's own path", calls[0].path)
	}
}

func TestDownloadRefusesMoreThanTheLimit(t *testing.T) {
	fake := newFakeAPI(t)
	fake.fileContent = []byte("0123456789")

	client := fake.client()

	// Exactly at the limit is allowed; one byte past it is not. A silently
	// truncated recording would transcribe to a sentence that stops mid-word.
	if _, err := client.Download(context.Background(), "voice/file_1.oga", 10); err != nil {
		t.Errorf("a file exactly at the limit should be allowed: %v", err)
	}
	if _, err := client.Download(context.Background(), "voice/file_1.oga", 9); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Download past the limit = %v, want ErrTooLarge", err)
	}
}

func TestDownloadRefusesAPathThatEscapes(t *testing.T) {
	fake := newFakeAPI(t)
	client := fake.client()

	// A string from somebody else's server, going straight into a URL path.
	for _, filePath := range []string{
		"",
		"/etc/passwd",
		"../../../etc/passwd",
		"voice/../../bot" + testToken + "/getMe",
		"https://elsewhere.example/thing",
	} {
		t.Run(filePath, func(t *testing.T) {
			if _, err := client.Download(context.Background(), filePath, 1<<20); err == nil {
				t.Errorf("Download(%q) was allowed", filePath)
			}
		})
	}

	if calls := fake.callsTo("download"); len(calls) != 0 {
		t.Errorf("a refused path still reached the server %d times", len(calls))
	}
}

func TestSendVoicePostsMultipart(t *testing.T) {
	fake := newFakeAPI(t)

	ogg := []byte("OggSOpusHead and then some audio")
	if err := fake.client().SendVoice(context.Background(), "4242", ogg, 12, "Heard you"); err != nil {
		t.Fatalf("SendVoice: %v", err)
	}

	calls := fake.callsTo("sendVoice")
	if len(calls) != 1 {
		t.Fatalf("sendVoice called %d times", len(calls))
	}
	body := calls[0].body
	if body["chat_id"] != "4242" {
		t.Errorf("chat id = %v", body["chat_id"])
	}
	if body["duration"] != "12" {
		t.Errorf("duration = %v", body["duration"])
	}
	if body["caption"] != "Heard you" {
		t.Errorf("caption = %v", body["caption"])
	}
	// The audio has to arrive as a file part, or Telegram treats the request
	// as a reference to a file id it does not have.
	if body["voice"] != string(ogg) {
		t.Errorf("voice part = %q, want the audio", body["voice"])
	}
	if body["voice_filename"] == "" {
		t.Error("the voice part was sent without a filename")
	}
}

func TestSendVoiceTruncatesALongCaption(t *testing.T) {
	fake := newFakeAPI(t)

	long := strings.Repeat("a", maxCaptionChars*2)
	if err := fake.client().SendVoice(context.Background(), "1", []byte("OggS"), 0, long); err != nil {
		t.Fatalf("SendVoice: %v", err)
	}

	caption, _ := fake.callsTo("sendVoice")[0].body["caption"].(string)
	// Losing the voice note entirely because its caption was too long would
	// be the wrong trade.
	if len([]rune(caption)) > maxCaptionChars {
		t.Errorf("caption is %d runes, past the %d limit", len([]rune(caption)), maxCaptionChars)
	}
}

func TestSendVoiceRefusesNothing(t *testing.T) {
	fake := newFakeAPI(t)
	if err := fake.client().SendVoice(context.Background(), "1", nil, 0, ""); err == nil {
		t.Error("expected an error for empty audio")
	}
}

func TestMediaErrorsNeverCarryTheToken(t *testing.T) {
	// The token travels in the URL path on every one of these, so a wrapped
	// *url.Error would print it. That is this package's whole design premise.
	fake := newFakeAPI(t)
	fake.failMethod("getFile", 400)
	fake.failMethod("sendVoice", 400)
	fake.fileStatus = 500
	client := fake.client()

	_, err := client.GetFile(context.Background(), "abc")
	checkNoToken(t, "GetFile", err)

	_, err = client.Download(context.Background(), "voice/file_1.oga", 1<<20)
	checkNoToken(t, "Download", err)

	checkNoToken(t, "SendVoice", client.SendVoice(context.Background(), "1", []byte("OggS"), 0, ""))

	// And the same once the server is gone entirely, which is the transport
	// error path where url.Error would be the tempting thing to wrap.
	fake.server.Close()
	_, err = client.Download(context.Background(), "voice/file_1.oga", 1<<20)
	checkNoToken(t, "Download after the server went away", err)
	checkNoToken(t, "SendVoice after the server went away",
		client.SendVoice(context.Background(), "1", []byte("OggS"), 0, ""))
}

func checkNoToken(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error", what)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("%s leaked the bot token: %v", what, err)
	}
}
