# LinkedIn drafts

Draft posts for the Congopro company page. Not tasks — moved out of TODO.md.

---

Ces derniers jours de R&D marquent un tournant !

Nous ne validons pas seulement une infrastructure technique, mais un modèle économique revu et adapté aux nouveaux besoins du marché.

Le dossier d'architecture initial est bouclé.
Mon équipe attend désormais le "Sprint Planning" pour lancer la roadmap et attaquer nos objectifs de front.

---

La stack est bâtie pour la performance (et rien d'autre) !

Pour répondre aux exigences de scalabilité du projet, mes choix technologiques sont guidés par une règle simple : la performance brute.

* Données & Stockage : Une architecture entièrement distribuée (base de données et stockage type S3).
* Flux de données : Un broker de messages distribué. Ici, le scope est restreint : seuls les outils écrits en langages système (comme Redpanda ou NATS) ont droit de cité. Si ce n'est pas optimisé pour la performance système, ce n'est pas retenu.
* Accessibilité : Une API Management robuste pour servir nos frontends (Web, Mobile, Desktop et bot Telegram).

---

Simplifier pour mieux scaler !

L'expérience m'a appris que la simplicité est la sophistication suprême.
Depuis nos premières notes, nous avons simplifié le pipeline de recherche :

* Avant : Trois briques distinctes (Full-text, Vectoriel avec traitements spécifiques, et Génératif via Ollama).
* Maintenant : Une intégration plus fluide où la recherche sémantique (Vectorisation) est aussi déléguée à Ollama.
  * Résultat : Moins de friction/code, plus de pertinence.

---

La sécurité des prompts !

Intégrer l'IA, c'est bien. La sécuriser, c'est indispensable.
Un des gros challenges à venir, qui méritera sans doute un podcast ou une série d'articles dédiés, est la sécurité autour des prompts IA (Prompt Injection, Data Leakage, etc.).
Un sujet crucial pour garantir la confidentialité des données économiques.

L'objectif final reste inchangé : fournir ce qui se fait de mieux pour l'analyse des données économiques locales.
Nous arrivons avec des rapports B2B augmentés par notre IA générative pour transformer la donnée brute en levier de croissance.

Let's go ! 🚀
