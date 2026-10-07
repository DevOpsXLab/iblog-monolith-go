package main

type person struct {
	Username, Name, Bio, Avatar string
	Tags                        []string // followed tags
}

// people are fictional; avatars are generated from the username.
var people = []person{
	{"azizbek", "Azizbek Karimov", "SRE @ fintech. Kubernetes, incident response va yaxshi uyqu tarafdori.", "", []string{"kubernetes", "sre"}},
	{"dilnoza", "Dilnoza Rashidova", "Platform engineer. Terraform, AWS va FinOps. Infratuzilma ham kod.", "", []string{"terraform", "aws", "platform-engineering"}},
	{"jasur", "Jasur Toshmatov", "Go backend dasturchi. Kichik binary'lar va katta yuklamalar.", "", []string{"go", "docker"}},
	{"malika", "Malika Yusupova", "DevSecOps muhandisi. Supply chain, sirlar boshqaruvi va threat modeling.", "", []string{"security", "devsecops"}},
	{"sardor", "Sardor Aliyev", "CI/CD va developer experience. Pipeline 5 daqiqadan uzoq bo'lmasligi kerak.", "", []string{"cicd", "github-actions", "gitops"}},
	{"nodira", "Nodira Ergasheva", "Observability engineer. OpenTelemetry, Grafana va SLO'lar.", "", []string{"observability", "opentelemetry", "sre"}},
	{"bekzod", "Bekzod Nazarov", "DBA/backend. PostgreSQL'ni sevaman, ORM'larga ishonmayman.", "", []string{"postgresql", "redis", "database"}},
	{"kamola", "Kamola Saidova", "Frontend engineer. Web performance va accessibility.", "", []string{"frontend", "performance"}},
	{"timur", "Timur Xasanov", "Linux sysadmin, 12 yillik tajriba. Tarmoq va xavfsizlik.", "", []string{"linux", "security"}},
	{"shoxrux", "Shoxrux Mirzayev", "Junior DevOps. O'rganyapman va o'rganganlarimni yozaman.", "", []string{"beginners", "kubernetes", "docker"}},
	{"gulnora", "Gulnora Abdullayeva", "QA automation engineer. Playwright va k6.", "", []string{"cicd", "performance"}},
	{"otabek", "Otabek Qodirov", "CTO @ startup. Arxitektura, jamoa va xarajatlar.", "", []string{"platform-engineering", "finops", "culture"}},
}

var categories = []string{"Kubernetes", "Docker", "CI/CD", "Cloud", "Observability", "Xavfsizlik", "Ma'lumotlar bazasi", "Linux", "Dasturlash"}

var labels = []struct{ Name, Color string }{
	{"Qo'llanma", "#2563eb"},
	{"Tavsiya etiladi", "#16a34a"},
	{"Yangi boshlovchilar", "#f59e0b"},
	{"Chuqur tahlil", "#7c3aed"},
}

type pub struct {
	Slug, Name, Description, Owner string
	Members                        map[string]string // username -> role
}

var publications = []pub{
	{"devopsxlab-weekly", "DevOpsXLab Weekly", "Har hafta: Kubernetes, CI/CD va SRE bo'yicha amaliy maqolalar.", "azizbek",
		map[string]string{"sardor": "editor", "jasur": "writer"}},
	{"cloud-native-uz", "Cloud Native UZ", "O'zbek tilidagi cloud-native hamjamiyat blogi: bulut, Linux va observability.", "dilnoza",
		map[string]string{"nodira": "editor", "timur": "writer"}},
}

var seriesDefs = []struct{ Key, Owner, Title, Description string }{
	{"kubernetes-noldan", "azizbek", "Kubernetes noldan", "Pod'dan HTTPS'li ilovagacha: uch qismli amaliy kurs."},
	{"postgres-prod", "bekzod", "PostgreSQL ishlab chiqarishda", "Indekslar, migratsiyalar va prod'da yashab qolish."},
}

// follows: follower -> followees
var follows = map[string][]string{
	"azizbek": {"dilnoza", "nodira", "sardor", "malika"},
	"dilnoza": {"azizbek", "timur", "otabek", "nodira"},
	"jasur":   {"bekzod", "azizbek", "sardor"},
	"malika":  {"timur", "dilnoza", "azizbek"},
	"sardor":  {"azizbek", "jasur", "gulnora"},
	"nodira":  {"azizbek", "dilnoza", "bekzod"},
	"bekzod":  {"jasur", "nodira"},
	"kamola":  {"jasur", "sardor", "nodira"},
	"timur":   {"malika", "azizbek"},
	"shoxrux": {"azizbek", "dilnoza", "jasur", "malika", "sardor", "nodira", "bekzod", "timur"},
	"gulnora": {"sardor", "kamola", "nodira"},
	"otabek":  {"dilnoza", "azizbek", "bekzod", "malika"},
}

type seedComment struct {
	Post   string // post file key, e.g. "01"
	Author string
	Text   string
	Reply  int // 1-based index of the parent within the same post; 0 = top level
}

var comments = []seedComment{
	{"01", "shoxrux", "Juda tushunarli yozilgan, rahmat! maxUnavailable: 0 haqidagi izoh aynan kerak edi — biz deploy paytida 502 olayotgan edik.", 0},
	{"01", "azizbek", "Arzimaydi! Readiness probe ham to'g'ri sozlanganini tekshiring, aks holda maxUnavailable yordam bermaydi. Keyingi qismda shu haqda.", 1},
	{"01", "gulnora", "Lokal sinash uchun kind yoki k3d tavsiya qilasizmi?", 0},
	{"01", "sardor", "kind CI uchun zo'r, lokal ish uchun men OrbStack'dagi k8s'ni ishlataman — juda yengil.", 3},
	{"02", "jasur", "Liveness'da DB tekshirmaslik — oltin qoida. Bir marta butun klasterni shu sababli restart loop'ga tushirganmiz.", 0},
	{"02", "malika", "Secret haqidagi eslatma uchun rahmat. Qo'shimcha: RBAC'da secrets uchun get/list huquqini ham cheklang, ko'pchilik unutadi.", 0},
	{"02", "shoxrux", "startupProbe'dagi failureThreshold: 30 va periodSeconds: 2 — demak ilovaga 60 soniya beriladi, to'g'rimi?", 0},
	{"02", "azizbek", "Ha, aynan. Java ilovalar uchun ba'zan 120+ soniya kerak bo'ladi.", 3},
	{"03", "timur", "HTTP-01 o'rniga DNS-01 ham ko'rib chiqing — wildcard sertifikat olish mumkin va 80-port ochiq bo'lishi shart emas.", 0},
	{"03", "dilnoza", "Gateway API'ga o'tishni rejalashtiryapmiz. Kelajakda shu haqda ham yozsangiz zo'r bo'lardi.", 0},
	{"04", "otabek", "State'ni bo'laklash bo'yicha bizda ham xuddi shunday tuzilma. apps/ qatlamida har servis o'z state'ida — plan 20 soniyada.", 0},
	{"04", "bekzod", "manage_master_user_password haqida bilmagan ekanman. Darhol joriy qilamiz.", 0},
	{"04", "dilnoza", "Juda yaxshi! Rotatsiya ham avtomatik bo'ladi, faqat ilova Secrets Manager'dan o'qishi kerak.", 2},
	{"05", "otabek", "NAT Gateway bo'yicha bizda ham xuddi shu holat edi. Gateway endpoint birinchi oydayoq o'zini oqladi.", 0},
	{"05", "jasur", "Graviton'ga o'tishda CGO ishlatadigan kutubxonalar bilan muammo bo'lmadimi?", 0},
	{"05", "dilnoza", "Bitta servisda librdkafka bor edi — o'sha uchun alohida arm64 base image yig'dik. Qolganlari toza Go edi.", 2},
	{"06", "bekzod", "preStop sleep haqida bilmasdim. Shu paytgacha image ichiga busybox qo'shib, sleep chaqirardik.", 0},
	{"06", "azizbek", "Ajoyib maqola. Qo'shimcha: terminationGracePeriodSeconds'ni HPA scale-down bilan ham tekshirish kerak.", 0},
	{"06", "kamola", "Frontend tomonidan ham tasdiqlayman — deploy paytidagi tasodifiy xatolar shu bilan yo'qoldi.", 0},
	{"07", "malika", "distroless + nonroot — xavfsizlik nuqtai nazaridan eng yaxshi start. trivy natijasini ham qo'shsangiz bo'lardi.", 0},
	{"07", "jasur", "Bizning image'da trivy 0 ta CVE ko'rsatdi :) Keyingi postda batafsil yozaman.", 1},
	{"07", "shoxrux", "--mount=type=cache GitHub Actions'da ham ishlaydimi?", 0},
	{"07", "sardor", "Ha, lekin runner'lar orasida saqlanmaydi. Buning uchun cache-to: type=gha ishlating — mening CI haqidagi postimda bor.", 3},
	{"08", "timur", "Kyverno siyosati misoli juda foydali. Biz hozircha audit rejimida sinab ko'ryapmiz.", 0},
	{"08", "malika", "To'g'ri yondashuv. Bir-ikki hafta audit, keyin Enforce. Aks holda birinchi kunida kimningdir deploy'i to'xtaydi.", 1},
	{"08", "otabek", "Startup uchun bu qanchalik zarur? Bizda hozircha 6 ta servis.", 0},
	{"08", "malika", "SBOM va grype — hozirdan, bu bir soatlik ish. Imzo va Kyverno — mijozlar compliance so'ray boshlaganda.", 3},
	{"09", "shoxrux", "Rotate birinchi, tarixni tozalash keyin — mana shu tartibni hamma bilishi kerak.", 0},
	{"09", "sardor", "GitHub push protection'ni barcha repo'larda tashkilot darajasida yoqish mumkin, bitta sozlama.", 0},
	{"10", "gulnora", "Testlarni shard qilish bizda Playwright uchun ham juda yordam berdi — 14 daqiqadan 4 ga.", 0},
	{"10", "jasur", "concurrency bilan cancel-in-progress — eng arzon va eng foydali o'zgarish.", 0},
	{"10", "otabek", "Kattaroq runner haqidagi fikr juda to'g'ri. Dasturchi vaqti runner vaqtidan ancha qimmat.", 0},
	{"11", "azizbek", "selfHeal'ni yoqishdan oldin jamoaga albatta ayting. Aks holda kimdir kubectl edit qiladi va o'zgarishi 3 daqiqada yo'qolganiga hayron bo'ladi :)", 0},
	{"11", "sardor", "Ha, bu bizda ham bo'lgan. Hozir README'da katta qizil harflar bilan yozilgan.", 1},
	{"11", "dilnoza", "Flux bilan solishtirsangiz qaysi birini tanlagan bo'lardingiz?", 0},
	{"12", "jasur", "otelpgx bilan har bir SQL so'rov trace'da ko'rinishi — debugging'ni butunlay o'zgartirdi.", 0},
	{"12", "kamola", "Frontend'dan ham trace boshlash mumkinmi? Brauzerdan backend'gacha bitta trace?", 0},
	{"12", "nodira", "Ha, @opentelemetry/sdk-trace-web bilan. traceparent header'ni fetch'ga qo'shadi. CORS'da shu header'ga ruxsat bering.", 2},
	{"13", "azizbek", "140 → 5 alert. Navbatchi sifatida bu maqolani ramkaga solib qo'yaman.", 0},
	{"13", "otabek", "SLO'ni biznes bilan kelishish eng qiyin qismi. 99.99 so'rashadi, lekin narxini bilishmaydi.", 0},
	{"13", "nodira", "Shuning uchun men har doim xato byudjetini daqiqalarda ko'rsataman: 99.99% = oyiga 4 daqiqa. Odatda gap shu yerda tugaydi.", 2},
	{"14", "jasur", "Keyset pagination — bizning API'da ham shunga o'tdik. Chuqur sahifalar 2 soniyadan 5 ms'ga tushdi.", 0},
	{"14", "shoxrux", "pg_stat_statements'ni yoqish uchun restart kerakmi?", 0},
	{"14", "bekzod", "Ha, shared_preload_libraries'ga qo'shish uchun bir marta restart kerak. RDS'da parameter group orqali.", 2},
	{"15", "jasur", "lock_timeout — har bir migratsiyaning birinchi qatori bo'lishi kerak. Biz goose'da buni shablonga qo'shdik.", 0},
	{"15", "azizbek", "Lock navbati haqidagi tushuntirish juda aniq. Ko'pchilik muammo migratsiyaning o'zida deb o'ylaydi.", 0},
	{"16", "jasur", "singleflight — standart kutubxonaning eng kam baholangan paketi.", 0},
	{"16", "kamola", "Kalitni versiyalash g'oyasi zo'r, deploy'dan keyingi g'alati xatolar shundan ekan.", 0},
	{"17", "shoxrux", "Shu ro'yxatni cheat sheet qilib chiqarib oldim. Rahmat!", 0},
	{"17", "azizbek", "cpu.stat'dagi nr_throttled — juda ko'p muammolarning sababi. CPU limit'larni olib tashlaganimizdan keyin p99 ikki barobar yaxshilandi.", 0},
	{"17", "timur", "Aynan! Memory limit qoldiring, CPU uchun faqat request.", 2},
	{"18", "malika", "SSH CA — eng kam ishlatiladigan, lekin eng foydali imkoniyat. step-ca bilan juda oson.", 0},
	{"18", "shoxrux", "\"Joriy sessiyani yopmang\" — buni qiyin yo'l bilan o'rganganman :)", 0},
	{"19", "jasur", "INP haqida birinchi marta tushunarli tushuntirish o'qidim.", 0},
	{"19", "gulnora", "Lighthouse CI'ni pipeline'ga qo'shish ham foydali, lekin to'g'ri aytdingiz — RUM o'rnini bosmaydi.", 0},
	{"20", "otabek", "Bu shablonni butun kompaniyada joriy qilamiz. \"Ehtiyot bo'lish kerak — harakat emas\" ayniqsa yoqdi.", 0},
	{"20", "nodira", "Vaqt chizig'idagi 17:40 → 17:58 bo'shlig'i — alerting ishimizning asosiy sababi bo'ldi.", 0},
	{"20", "sardor", "Avtomatik rollback tayyor, Argo Rollouts analysis bilan. Keyingi haftada yozaman.", 0},
	{"21", "shoxrux", "compose watch haqida bilmagan ekanman, nodemon o'rniga juda qulay.", 0},
	{"21", "kamola", "Frontend uchun ham ishlaydi, Vite HMR bilan birga.", 1},
	{"22", "otabek", "\"Platforma portal emas — olib tashlangan qo'l mehnati\". Shu jumla uchun rahmat.", 0},
	{"22", "azizbek", "Crossplane'ni prod'da qancha vaqtdan beri ishlatyapsizlar? Upgrade'lar qanday o'tyapti?", 0},
	{"22", "dilnoza", "8 oy. Provider upgrade'lari ba'zan og'riqli, shuning uchun staging klasterda bir hafta ushlab turamiz.", 2},
}

// lists: owner -> name, description, post keys
var lists = []struct {
	Owner, Name, Description string
	Posts                    []string
}{
	{"shoxrux", "Junior DevOps uchun", "DevOps'ni boshlayotganlarga tavsiya qilaman", []string{"01", "02", "03", "17", "21", "09"}},
	{"otabek", "CTO o'qishi kerak", "Arxitektura, xarajat va madaniyat", []string{"05", "13", "20", "22", "04"}},
	{"jasur", "Go va backend", "", []string{"06", "07", "14", "15", "16"}},
}

// pins: author -> post key
var pins = map[string]string{"azizbek": "01", "dilnoza": "05", "bekzod": "14", "malika": "08"}
