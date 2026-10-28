// Package station runs a station's day: what the boards and announcements say, which
// platform each train uses (and where they clash), where each coach stops, how busy
// it will be, and the facilities desk.
package station

import (
	"fmt"
	"strings"
)

// Announcement kinds, in the order a train goes through them.
const (
	KindDelayed  = "delayed"
	KindPlatform = "platform_changed"
	KindArriving = "arriving"
	KindStanding = "on_platform"
	KindLeaving  = "departing"
)

// Line is one language's version of an announcement. Text is for the display;
// Speech spells the train number digit by digit, the way stations say it.
type Line struct {
	Lang   string `json:"lang"`
	BCP47  string `json:"bcp47"` // for the browser's speech synthesis voice
	Text   string `json:"text"`
	Speech string `json:"speech"`
}

type facts struct {
	Kind, Number, Name, From, To, Time string
	Platform, OldPlatform, Delay       int
}

var bcp47 = map[string]string{"en": "en-IN", "hi": "hi-IN", "ta": "ta-IN", "ml": "ml-IN"}

// Templates exist for English, Hindi, Tamil and Malayalam. Other regional languages
// fall back to Hindi and English (their templates still need writing, ideally with
// native speakers). Station and train names stay in English, as on Indian boards.
var templates = map[string]map[string]string{
	"en": {
		KindArriving: "May I have your attention please. Train number {n}, {name}, from {from} to {to}, is arriving shortly on platform number {p}.",
		KindStanding: "Train number {n}, {name}, from {from} to {to}, is standing on platform number {p}.",
		KindDelayed:  "May I have your attention please. Train number {n}, {name}, from {from} to {to}, is running late by about {d} minutes. It is now expected at {time} on platform number {p}. We regret the inconvenience caused.",
		KindPlatform: "Kindly note the change of platform. Train number {n}, {name}, will now arrive on platform number {p} instead of platform number {old}.",
		KindLeaving:  "Train number {n}, {name}, to {to}, is about to leave from platform number {p}.",
	},
	"hi": {
		KindArriving: "यात्रीगण कृपया ध्यान दें। {from} से चलकर {to} को जाने वाली गाड़ी संख्या {n}, {name}, थोड़ी ही देर में प्लेटफ़ॉर्म संख्या {p} पर आ रही है।",
		KindStanding: "{from} से चलकर {to} को जाने वाली गाड़ी संख्या {n}, {name}, प्लेटफ़ॉर्म संख्या {p} पर खड़ी है।",
		KindDelayed:  "यात्रीगण कृपया ध्यान दें। {from} से चलकर {to} को जाने वाली गाड़ी संख्या {n}, {name}, लगभग {d} मिनट की देरी से चल रही है। अब यह {time} बजे प्लेटफ़ॉर्म संख्या {p} पर आएगी। आपको हुई असुविधा के लिए हमें खेद है।",
		KindPlatform: "यात्रीगण कृपया ध्यान दें। गाड़ी संख्या {n}, {name}, अब प्लेटफ़ॉर्म संख्या {old} के स्थान पर प्लेटफ़ॉर्म संख्या {p} पर आएगी।",
		KindLeaving:  "{to} को जाने वाली गाड़ी संख्या {n}, {name}, प्लेटफ़ॉर्म संख्या {p} से छूटने के लिए तैयार है।",
	},
	"ta": {
		KindArriving: "பயணிகளின் கனிவான கவனத்திற்கு. {from} இலிருந்து {to} செல்லும் வண்டி எண் {n}, {name}, சிறிது நேரத்தில் நடைமேடை எண் {p} க்கு வந்து சேரும்.",
		KindStanding: "{from} இலிருந்து {to} செல்லும் வண்டி எண் {n}, {name}, நடைமேடை எண் {p} இல் நின்று கொண்டிருக்கிறது.",
		KindDelayed:  "பயணிகளின் கனிவான கவனத்திற்கு. {from} இலிருந்து {to} செல்லும் வண்டி எண் {n}, {name}, சுமார் {d} நிமிடங்கள் தாமதமாக வந்து கொண்டிருக்கிறது. இப்போது {time} மணிக்கு நடைமேடை எண் {p} க்கு வரும் என எதிர்பார்க்கப்படுகிறது. ஏற்பட்ட சிரமத்திற்கு வருந்துகிறோம்.",
		KindPlatform: "பயணிகளின் கனிவான கவனத்திற்கு. வண்டி எண் {n}, {name}, நடைமேடை எண் {old} க்கு பதிலாக நடைமேடை எண் {p} க்கு வந்து சேரும்.",
		KindLeaving:  "{to} செல்லும் வண்டி எண் {n}, {name}, நடைமேடை எண் {p} இலிருந்து புறப்படத் தயாராக உள்ளது.",
	},
	"ml": {
		KindArriving: "യാത്രക്കാരുടെ ശ്രദ്ധയ്ക്ക്. {from} ൽ നിന്ന് {to} ലേക്ക് പോകുന്ന ട്രെയിൻ നമ്പർ {n}, {name}, അൽപ്പസമയത്തിനകം പ്ലാറ്റ്ഫോം നമ്പർ {p} ൽ എത്തിച്ചേരും.",
		KindStanding: "{from} ൽ നിന്ന് {to} ലേക്ക് പോകുന്ന ട്രെയിൻ നമ്പർ {n}, {name}, പ്ലാറ്റ്ഫോം നമ്പർ {p} ൽ നിർത്തിയിട്ടിരിക്കുന്നു.",
		KindDelayed:  "യാത്രക്കാരുടെ ശ്രദ്ധയ്ക്ക്. {from} ൽ നിന്ന് {to} ലേക്ക് പോകുന്ന ട്രെയിൻ നമ്പർ {n}, {name}, ഏകദേശം {d} മിനിറ്റ് വൈകിയാണ് ഓടുന്നത്. {time} ന് പ്ലാറ്റ്ഫോം നമ്പർ {p} ൽ എത്തുമെന്ന് പ്രതീക്ഷിക്കുന്നു. ഉണ്ടായ അസൗകര്യത്തിൽ ഖേദിക്കുന്നു.",
		KindPlatform: "യാത്രക്കാരുടെ ശ്രദ്ധയ്ക്ക്. ട്രെയിൻ നമ്പർ {n}, {name}, പ്ലാറ്റ്ഫോം നമ്പർ {old} ന് പകരം പ്ലാറ്റ്ഫോം നമ്പർ {p} ൽ എത്തിച്ചേരും.",
		KindLeaving:  "{to} ലേക്ക് പോകുന്ന ട്രെയിൻ നമ്പർ {n}, {name}, പ്ലാറ്റ്ഫോം നമ്പർ {p} ൽ നിന്ന് പുറപ്പെടാൻ തയ്യാറാണ്.",
	},
}

// Languages lists the announcement order for a station: its regional language (if a
// template exists), then Hindi, then English — skipping repeats.
func Languages(regional string) (langs []string, missing string) {
	seen := map[string]bool{}
	for _, l := range []string{regional, "hi", "en"} {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		if _, ok := templates[l]; !ok {
			missing = l
			continue
		}
		langs = append(langs, l)
	}
	return langs, missing
}

func spellDigits(n string) string { return strings.Join(strings.Split(n, ""), " ") }

func render(lang string, f facts, number string) string {
	r := strings.NewReplacer("{n}", number, "{name}", f.Name, "{from}", f.From, "{to}", f.To, "{time}", f.Time,
		"{p}", fmt.Sprint(f.Platform), "{old}", fmt.Sprint(f.OldPlatform), "{d}", fmt.Sprint(f.Delay))
	return r.Replace(templates[lang][f.Kind])
}

// Compose writes an announcement in each of the station's languages.
func Compose(regional string, f facts) []Line {
	langs, _ := Languages(regional)
	out := make([]Line, 0, len(langs))
	for _, l := range langs {
		out = append(out, Line{Lang: l, BCP47: bcp47[l], Text: render(l, f, f.Number), Speech: render(l, f, spellDigits(f.Number))})
	}
	return out
}
