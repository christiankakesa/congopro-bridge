# Tasks

* Now, I need your help to elaborate a clear, concise, and solid description of everything required to make this big project go live.
I mainly mean the backend architecture, infrastructure, and systems needed to turn this project into a real and successful platform.
We will iterate together while you ask me questions, and I guide the architecture, technical decisions, and overall direction.
Here are the bullet points I currently have to start with.
  * CMS to manage:
    * ADS and ADS monetization
    * Company info: add, update, merge, de-duplicate, ...
    * GPS location for all entries in the CMS or ad-hoc script
    * Users and permissions
    * customers/clients to manage their data.
    * Dispute systems for people that claim same companies
    * Is Telegram app capabilities good to build a support (ticketing) system app?
    * Is Telegram app capabilities good to build data management for the customers and the team
    * I need simple but secure technologies like rqlite as database cluster or pocketbase cluster to store all data and provide local data for all frontends. The goal is to build an African project and each countries will have their frontend with their filtered companies. I need to manage a paid subscription projects out of Arfica that sell or buy in Africa and need to listed as promoted buissiness in home page like ADS in search results.
    * I will need to know protocole that are used to store official information for comapnies to be compatuble with .
    * and more 
* 
* 

## DONE

* Implement "loading active Homepage AD" with a very good and luxury or high design looking.
* Build a social app banner and create a ADS campaign
* Build new logo and web SEO logo (Daffa)
* Make the internal ads feature print 1 ADS 75% of page load, and 2 ADS 25% of page load
* Build /sitemap.xml.gz that adds static pages links (/, /help, /privacy; /terms) and /company/[name_seo]
* design a new logo with Banana from Google.
* build a resulsts like a moderne Google Search. No default companies shown on load.
* add a coockie aceptance that says that we use partners sdk for security and analytics purpose and never sell your data. or something better.
* add a simple mecanism of ads based on a yaml files with parameters like: active/inactive, perdiod, rotation time, etc...
* create a make file with ssh, rsync capability: traefic dynamic entry, systemd service start
* Handle query like:
  * companies: (/(fr|en))?/company/congo-futur-sprl => query "name_seo" exact match
  * Redirect to "/":
    * subscriptions: (/(fr|en))?/subscriptions
  * Pages:
    * Same page:
      * about: (/(fr|en))?/about
      * contact: (/(fr|en))?/contact
      * faq: (/(fr|en))?/faq
      * help: (/(fr|en))?/help
    * privacy: (/(fr|en))?/privacy
    * terms: (/(fr|en))?/terms
      var langCompanyPathRegex = regexp.MustCompile(`^(/(fr|en))?/company/congo-futur-sprl$`)

      func handler(w http.ResponseWriter, r *http.Request) {
          if langCompanyPathRegex.MatchString(r.URL.Path) {
              fmt.Fprintln(w, "Path matches!")
          } else {
              http.NotFound(w, r)
          }
      }

      matches := langCompanyPathRegex.FindStringSubmatch(r.URL.Path)
      if matches != nil {
          lang := matches[2] // "fr", "en", or empty string
          // ...
      }

* 

## LinkedIn reposts

Ces derniers jours de R&D marquent un tournant !
Nous ne validons pas seulement une infrastructure technique, mais un modèle économique revu et adapté aux nouveaux besoins du marché.

Le dossier d’architecture initial est bouclé.
Mon équipe attend désormais le "Sprint Planning" pour lancer la roadmap et attaquer nos objectifs de front.

La stack est bâtie pour la performance (et rien d'autre) !
Pour répondre aux exigences de scalabilité du projet, mes choix technologiques sont guidés par une règle simple : la performance brute.

* Données & Stockage : Une architecture entièrement distribuée (base de données et stockage type S3).
* Flux de données : Un broker de messages distribué. Ici, le scope est restreint : seuls les outils écrits en langages système (comme Redpanda ou NATS) ont droit de cité. Si ce n'est pas optimisé pour la performance système, ce n'est pas retenu.
* Accessibilité : Une API Management robuste pour servir nos frontends (Web, Mobile, Desktop et bot Telegram).

Simplifier pour mieux scaler !
L'expérience m'a appris que la simplicité est la sophistication suprême.
Depuis mes premières notes, nous avons simplifié le pipeline de recherche :

* Avant : Trois briques distinctes (Full-text, Vectoriel avec traitements spécifiques, et Génératif via Ollama).
* Maintenant : Une intégration plus fluide où la recherche sémantique (Vectorisation) est aussi déléguée à Ollama.
  * Résultat : Moins de friction/code, plus de pertinence.

La sécurité des prompts !
Intégrer l'IA, c'est bien. La sécuriser, c'est indispensable.
L'un des gros challenges à venir, qui méritera sans doute un podcast ou une série d'articles dédiés, est la sécurité autour des prompts IA (Prompt Injection, Data Leakage, etc.).
Un sujet crucial pour garantir la confidentialité des données économiques.

L'objectif final reste inchangé : fournir ce qui se fait de mieux pour l'analyse des données économiques locales.
Nous arrivons avec des rapports B2B augmentés par notre IA générative pour transformer la donnée brute en levier de croissance.

Let’s go ! 🚀

## PageSpeed

* https://pagespeed.web.dev/analysis/https-congopro-com/loz8e4kjae?hl=fr&form_factor=mobile
* https://pagespeed.web.dev/analysis/https-congopro-com/1w6laz73ws?utm_source=search_console&form_factor=mobile&hl=fr

## Message in French

Hello mon réseau 👋

Je suis actuellement en train de refondre le projet Congopro afin de le remettre au goût du jour et surtout l’adapter aux opportunités liées à l’émergence du continent africain.

Ma vision et la direction du projet sont détaillées ici :
[Article LinkedIn – Congopro évolue](https://www.linkedin.com/pulse/congopro-%C3%A9volue-congopro-erfvc/)

Une première étape concrète est déjà en ligne :
[Congopro](https://congopro.com/)

L’objectif est ambitieux : construire une plateforme moderne capable d’accompagner la visibilité, la découverte et la croissance des entreprises africaines à grande échelle.

Je suis aujourd’hui à la recherche de collaborateurs, partenaires et profils motivés pour rejoindre cette aventure, aussi bien sur les aspects :
• réseau & développement stratégique
• technologie & produit
• financement & accompagnement à la croissance

Si le projet vous parle, n’hésitez pas à me contacter ou à partager ce post autour de vous 🙏

Merci à tous pour votre soutien.
