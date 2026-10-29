package dataset

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRegionalLanguage(t *testing.T) {
	w := Generate(Large())
	seen := map[string]map[string]bool{}
	usernames := map[string]bool{}
	displayNames := map[string]bool{}
	kinds := map[string]map[bool]int{}
	pattern := regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	for index, u := range w.Users {
		if !pattern.MatchString(u.Username) || usernames[u.Username] {
			t.Fatalf("invalid or duplicate username %q", u.Username)
		}
		usernames[u.Username] = true

		l := languageFor(u.Region, u.ID)
		if seen[u.Region] == nil {
			seen[u.Region] = map[string]bool{}
		}
		seen[u.Region][l.code] = true
		baseName := strings.TrimSuffix(u.DisplayName, " "+strconv.Itoa(index))
		if displayNames[u.DisplayName] {
			t.Fatalf("duplicate display name %q", u.DisplayName)
		}
		displayNames[u.DisplayName] = true
		personal := slices.Contains(l.personalNames, baseName)
		key := u.Region + "/" + l.code
		if kinds[key] == nil {
			kinds[key] = map[bool]int{}
		}
		kinds[key][personal]++
		if !personal && !slices.Contains(l.names, baseName) {
			t.Fatalf("%s: unexpected name %q", l.code, u.DisplayName)
		}
		if u.Bio != "" && !slices.Contains(l.bios, u.Bio) {
			t.Fatalf("%s: unexpected bio %q", l.code, u.Bio)
		}
		names, handles := l.names, l.handles
		if personal {
			names, handles = l.personalNames, l.personalHandles
		}
		team := slices.Index(names, baseName)
		valid := u.Username == handles[team]+"_"+strconv.Itoa(index)
		if !valid {
			t.Fatalf("%s: unexpected username %q", l.code, u.Username)
		}
	}
	for _, region := range Regions {
		if len(seen[region]) != 3 {
			t.Fatalf("%s languages: %v", region, seen[region])
		}
	}
	for key, set := range kinds {
		if len(set) != 2 || set[true] < set[false] || set[true] > set[false]+1 {
			t.Fatalf("%s must contain ordinary and invented names", key)
		}
	}
	crossLanguage := 0
	for day := range 3 {
		for _, p := range w.PostsOn(day) {
			author := w.Users[w.userIndex[p.AuthorID]]
			l := languageFor(author.Region, author.ID)
			if !slices.Contains(l.captions, p.Caption) {
				t.Fatalf("%s: unexpected caption %q", l.code, p.Caption)
			}
			if p.Location.Locality != nil && !slices.Contains(l.localities, *p.Location.Locality) {
				t.Fatalf("%s: unexpected locality", l.code)
			}
			for _, c := range p.Comments {
				commenter := w.Users[w.userIndex[c.AuthorID]]
				cl := languageFor(commenter.Region, commenter.ID)
				if cl.code != l.code {
					crossLanguage++
				}
				if !slices.Contains(cl.comments, c.Body) {
					t.Fatalf("comment uses wrong author's language: %s %q", cl.code, c.Body)
				}
			}
		}
	}
	if crossLanguage == 0 {
		t.Fatal("no cross-language comments exercised")
	}
}

func TestLanguageCatalog(t *testing.T) {
	for _, region := range Regions {
		if len(regionLanguages[region]) != 3 {
			t.Fatalf("%s needs three languages", region)
		}
		for _, l := range regionLanguages[region] {
			if len(l.names) != 20 || len(l.handles) != len(l.names) {
				t.Fatalf("%s missing translated team names", l.code)
			}
			if len(l.personalNames) != 10 || len(l.personalHandles) != len(l.personalNames) {
				t.Fatalf("%s missing ordinary names", l.code)
			}
			names, handles := map[string]bool{}, map[string]bool{}
			allNames := append(append([]string{}, l.names...), l.personalNames...)
			allHandles := append(append([]string{}, l.handles...), l.personalHandles...)
			pattern := regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
			for i, name := range allNames {
				if utf8.RuneCountInString(name) > 100 || names[name] {
					t.Fatalf("%s invalid or duplicate display name %q", l.code, name)
				}
				names[name] = true
				handle := allHandles[i]
				if !pattern.MatchString(handle+"_4999") || handles[handle] {
					t.Fatalf("%s invalid or duplicate handle %q", l.code, handle)
				}
				handles[handle] = true
			}
			for _, values := range [][]string{l.names, l.handles, l.personalNames, l.personalHandles, l.bios, l.comments, l.localities} {
				if len(values) == 0 {
					t.Fatalf("%s empty catalog", l.code)
				}
				for _, value := range values {
					if value == "" {
						t.Fatalf("%s empty value", l.code)
					}
				}
			}
			if len(l.captions) != 16 || len(l.comments) != 14 || len(l.localities) != 8 {
				t.Fatalf("%s changes historical random draws", l.code)
			}
			if !slices.Contains(l.captions, "") {
				t.Fatalf("%s missing empty caption", l.code)
			}
		}
	}
}

func TestNoOriginalTeamNames(t *testing.T) {
	originals := []string{
		"釜中の鯖",
		"百万円ドリブン",
		"カラアゲネイティブ",
		"白金動物園",
		"パカパカアルパカ",
		"ふんばり温泉チーム",
		"カレーおじさん",
		"くすサポISUCON部",
		"焼肉ジャンボチキン",
		"塩昆布",
		"天空の玉子",
		"ツナ缶",
		"ケーキ屋さん見習い",
		"うどんきれいなGo",
		"デュアル同好会",
		"さばかんちゃーはんかれー",
		"流れ弾",
		"たんぽぽの上の刺身",
		"とりあえずモヒート",
		"織時屋",
		"锅里的青花鱼",
		"一百万日元驱动",
		"天生炸鸡派",
		"白金动物园",
		"哒哒羊驼",
		"奋力温泉队",
		"咖喱大叔",
		"Kusu支持ISUCON社",
		"烤肉巨无霸鸡",
		"盐渍海带",
		"天空之蛋",
		"金枪鱼罐头",
		"蛋糕店学徒",
		"乌冬面与漂亮的Go",
		"双重爱好会",
		"鲭鱼罐头炒饭咖喱",
		"流弹",
		"蒲公英上的刺身",
		"先来杯莫吉托",
		"织时屋",
		"솥 안의 고등어",
		"백만 엔 드리븐",
		"타고난 가라아게파",
		"시로카네 동물원",
		"다그닥 알파카",
		"분발 온천 팀",
		"카레 아저씨",
		"쿠스 서포트 ISUCON부",
		"야키니쿠 점보 치킨",
		"소금 다시마",
		"하늘의 달걀",
		"참치 캔",
		"케이크 가게 견습생",
		"우동과 깔끔한 Go",
		"듀얼 동호회",
		"고등어 통조림 볶음밥 카레",
		"유탄",
		"민들레 위의 회",
		"일단 모히토",
		"시간을 짜는 집",
		"Mackerel in a Cauldron",
		"Million Yen Driven",
		"Fried Chicken Native",
		"Shirokane Zoo",
		"Clip-Clop Alpacas",
		"Funbari Hot Spring Team",
		"Curry Uncle",
		"Kusu Support ISUCON Club",
		"Yakiniku Jumbo Chicken",
		"Salted Kelp",
		"Egg of the Sky",
		"Canned Tuna",
		"Apprentice Cake Maker",
		"Udon and Clean Go",
		"Dual Enthusiasts Club",
		"Canned Mackerel Fried Rice Curry",
		"Stray Bullet",
		"Sashimi on a Dandelion",
		"A Mojito for Now",
		"Time Weavers",
		"Maquereau au chaudron",
		"Un million de yens comme moteur",
		"Poulet frit de naissance",
		"Zoo de Shirokane",
		"Alpagas au trot",
		"Équipe des sources de Funbari",
		"Tonton Curry",
		"Club ISUCON Kusu Support",
		"Poulet géant au barbecue",
		"Algue salée",
		"Œuf du ciel",
		"Thon en boîte",
		"Apprenti pâtissier",
		"Udon et Go bien propre",
		"Club des passionnés du double",
		"Curry de riz sauté au maquereau en boîte",
		"Balle perdue",
		"Sashimi sur un pissenlit",
		"Un mojito pour commencer",
		"Tisseurs du temps",
		"Makrele im Kessel",
		"Von einer Million Yen angetrieben",
		"Geboren fürs Brathähnchen",
		"Zoo von Shirokane",
		"Klippklapp-Alpakas",
		"Team Thermalquelle Funbari",
		"Curry-Onkel",
		"Kusu-Support-ISUCON-Club",
		"Jumbo-Grillhähnchen",
		"Gesalzener Seetang",
		"Ei des Himmels",
		"Thunfischdose",
		"Konditorlehrling",
		"Udon und sauberes Go",
		"Club der Dual-Fans",
		"Curry mit Bratreis und Dosenmakrele",
		"Verirrte Kugel",
		"Sashimi auf Löwenzahn",
		"Erst mal einen Mojito",
		"Zeitweber",
		"Caballa en el caldero",
		"Impulsados por un millón de yenes",
		"De pollo frito desde la cuna",
		"Zoo de Shirokane",
		"Alpacas al trote",
		"Equipo Termas de Funbari",
		"Tío Curry",
		"Club ISUCON Kusu Support",
		"Pollo gigante a la parrilla",
		"Alga salada",
		"Huevo del cielo",
		"Atún en lata",
		"Aprendiz de pastelero",
		"Udon y Go limpio",
		"Club de aficionados a lo dual",
		"Curry de arroz frito con caballa en lata",
		"Bala perdida",
		"Sashimi sobre un diente de león",
		"Un mojito por ahora",
		"Tejedores del tiempo",
		"Cavala no caldeirão",
		"Movidos por um milhão de ienes",
		"Frango frito de nascença",
		"Zoológico de Shirokane",
		"Alpacas a trote",
		"Equipe das Termas de Funbari",
		"Tio Curry",
		"Clube ISUCON Kusu Support",
		"Frango gigante na brasa",
		"Alga salgada",
		"Ovo do céu",
		"Atum em lata",
		"Aprendiz de confeiteiro",
		"Udon e Go limpo",
		"Clube dos fãs do dual",
		"Curry de arroz frito com cavala em lata",
		"Bala perdida",
		"Sashimi sobre um dente-de-leão",
		"Um mojito por enquanto",
		"Tecelões do tempo",
	}
	for _, languages := range regionLanguages {
		for _, l := range languages {
			for _, name := range append(append([]string{}, l.names...), l.personalNames...) {
				if slices.Contains(originals, name) {
					t.Errorf("original team name or direct translation retained: %q", name)
				}
			}
		}
	}
}
