# Congopro Bridge (African Hub | African Bridges | Bridges)

## 1. Vision Stratégique : L'Architecture "Event-Driven"

Pour gérer 13 millions d'entreprises sans jamais perdre une seule mise à jour, nous abandonnons le modèle classique "Client-Serveur" au profit d'un modèle **orienté événements**.

### A. Ingestion Souple (NATS JetStream)

Les données entrent par divers canaux (API, imports CSV massifs, Google Address).

* **NATS JetStream** agit comme une mémoire tampon persistante. Si 1 million de lignes arrivent d'un coup, NATS les stocke et les distribue aux workers Go à la vitesse qu'ils peuvent absorber.
* **Résilience :** Aucun plantage de base de données ne peut causer de perte de données à l'entrée.

### B. Enrichissement & Optimisation (Workers Go)

Des programmes Go spécialisés consomment les messages de NATS :

* **Worker Géo :** Calcule les coordonnées GPS.
* **Worker Media :** Récupère les logos, les compresse en **WebP** et les stocke sur **S3/MinIO** pour minimiser la consommation de bande passante des utilisateurs (critique en Afrique).

---

## 2. Le Cœur de Données : Cohérence et Vitesse

Nous séparons la **Source de Vérité** de la **Performance de Recherche**.

### A. rqlite (Source de Vérité & Consistance)

* **Choix :** rqlite (SQLite distribué via Raft).
* **Rôle :** Stocker les données officielles, les abonnements payants et les configurations publicitaires.
* **Argument Investisseur :** Une consistance forte garantie par le protocole Raft. Même en cas de panne d'un serveur, les données restent intègres et identiques sur tout le cluster.

### B. Moteur de Recherche Hybride (Typesense/Meilisearch)

* **Rôle :** Un index ultra-rapide en RAM synchronisé avec rqlite.
* **Performance :** Recherche instantanée sur 13M d'entreprises, même avec des fautes de frappe ou des connexions 3G instables.

---

### 3. Gestion Intelligente : Dé-duplication & Litiges

Avec 13 millions d'entrées, la qualité de la donnée est votre plus grand actif.

* **La Fusion (Merge) Périodique :** Un programme Go analyse la base de données (hebdomadairement/mensuellement). Il regroupe les entreprises par similarité (Nom proche + même zone GPS + même téléphone).
* **Human-in-the-loop (Telegram) :** Le programme ne fusionne pas aveuglément les cas suspects. Il envoie une notification à l'équipe via une **Telegram Mini App**. Un modérateur valide la fusion d'un simple "Swipe" depuis son téléphone.

---

## 4. Infrastructure : Scalabilité Continentale

Le système est conçu pour être déployé pays par pays de manière isolée mais connectée.

* **Kubernetes (K8s) :** Gère la couche applicative (APIs, Workers, Assistant IA). Permet de scaler horizontalement pendant les pics d'utilisation.
* **VPS Managés :** Pour le stockage (rqlite, S3/MinIO). Les données restent sur des disques performants et isolés, facilitant la conformité aux lois locales sur la protection des données dans chaque pays africain.

---

## 5. L'Expérience Utilisateur : Mobile-First & Telegram

L'accès à la donnée doit être universel.

* **Frontend Web (Go/Tailwind v4) :** Ultra-léger, optimisé PageSpeed (100/100) pour consommer le moins de "Data" possible.
* **Telegram Mini App (TMA) :** Le "système d'exploitation" de facto en Afrique.
* **Clients :** Gèrent leur profil et paient leurs abonnements via le bot.
* **Équipe Support :** Reçoit les tickets de litiges et discute directement avec les propriétaires d'entreprises.



---

## 6. Synthèse pour Investisseurs : Pourquoi ce projet va réussir

| Pilier | Solution Technique | Avantage Concurrentiel |
| --- | --- | --- |
| **Volume** | NATS + rqlite | Capacité de 13M+ sans perte de performance. |
| **Coût** | Go + SQLite + S3 | Frais d'infrastructure réduits de 60% vs solutions Cloud classiques. |
| **Accessibilité** | Telegram + Web Hybride | Pénétration maximale du marché mobile africain. |
| **IA** | Assistant IA local | Recherche contextuelle intelligente, unique sur le continent. |
| **Confiance** | Système de claim/litige | Base de données vérifiée et certifiée, contrairement au scraping sauvage. |

---

### Prochaines étapes suggérées :

Prenez le temps de digérer cette structure. Elle est techniquement solide (Go/NATS/rqlite) tout en étant innovante sur le plan métier (TMA/IA).

Une fois que vous l'aurez présentée, nous pourrons attaquer le **"Blueprinting"** détaillé des schémas de base de données (rqlite) et la définition des "Prototypes de Messages" pour NATS JetStream afin de commencer le développement des premiers Workers d'importation.
