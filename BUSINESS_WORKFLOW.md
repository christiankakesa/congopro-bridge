# Congopro — Guide d'accueil de l'équipe

*Document d'intégration : ce qu'est Congopro, ce que nous vendons, et comment
se fait le travail au quotidien. Mis à jour le 29 août 2026. Pour la partie
technique, voir `docs/ARCHITECTURE.md` ; pour l'historique des décisions,
`docs/TODO.md`.*

---

## 1. Qu'est-ce que Congopro ?

**Congopro ([congopro.com](https://congopro.com)) est le moteur de recherche
des entreprises de la République Démocratique du Congo.** Un visiteur tape
« banque à Kinshasa » ou le nom d'une société, et obtient instantanément des
fiches d'entreprises : activité, adresse, téléphone, e-mail, site web, réseaux
sociaux. La recherche est boostée à l'IA : elle comprend le sens de la requête
(pas seulement les mots exacts) et peut afficher une réponse rédigée en haut
des résultats.

Trois principes guident tout ce que nous faisons :

1. **Mobile d'abord, connexion faible d'abord.** Nos utilisateurs sont
   majoritairement sur téléphone, souvent avec une connexion lente et chère.
   Chaque page doit rester légère et rapide.
2. **La donnée est le produit.** Une fiche juste, complète et à jour vaut plus
   que n'importe quelle fonctionnalité. La qualité des données est le travail
   de toute l'équipe.
3. **Gratuit pour chercher, payant pour se démarquer.** La consultation est et
   restera gratuite. Les revenus viennent des entreprises qui veulent plus de
   visibilité.

### Ce que voit le public

| Page | Rôle |
|---|---|
| `congopro.com` | Accueil : barre de recherche, suggestions, publicités |
| `/company/<nom>` | Fiche entreprise : coordonnées, activité, badges |
| `/account` | Espace Entreprise : connexion des clients (dirigeants d'entreprises) |
| `/contact` | Formulaire de contact + lien vers le canal Telegram public |
| `/admin` | Notre back-office (réservé à l'équipe) |

---

## 2. Nos services et nos revenus

### A. La Mise en avant — 15 $ / mois (self-service)

Le produit payant principal, entièrement automatisé. Une entreprise qui a
**revendiqué sa fiche** (voir la réclamation, §4) peut souscrire depuis son
Espace Entreprise. Paiement par carte via Stripe, **sans engagement,
résiliable à tout moment**.

Ce que le client obtient :

- **Sa fiche remonte en tête des résultats de recherche** qui la concernent
  (au-dessus des résultats naturels, ordre de pertinence conservé entre
  fiches promues) ;
- un badge **« Promu »** sur sa fiche et dans les résultats.

Points à connaître pour répondre aux clients :

- L'activation est **immédiate** après le paiement (quelques secondes).
- En cas d'échec de paiement au renouvellement, la mise en avant reste active
  pendant une période de grâce (« paiement en retard ») avant résiliation.
- La résiliation se fait par le client lui-même ; la mise en avant s'arrête
  alors immédiatement, sans remboursement au prorata.
- Personne dans l'équipe ne manipule de numéro de carte, jamais : tout passe
  par la page de paiement Stripe.

### B. Les Publicités (vente directe)

Des encarts publicitaires vendus **de gré à gré** par l'équipe (il n'y a pas
de self-service). Deux emplacements :

- **Accueil** : visible avant toute recherche ;
- **Résultats de recherche** : affichée selon des mots-clés (une pub « ciment »
  sort sur les recherches liées au BTP).

Chaque campagne a une période de diffusion, un poids (plus le poids est
élevé, plus elle sort souvent), un prix négocié et **un vendeur attribué** —
c'est ainsi qu'on suit qui a vendu quoi. Un interrupteur général permet de
couper toute la diffusion en urgence (`/admin/ads` → réglages).

### C. Ce qui est gratuit et le reste

La recherche, les fiches, la réclamation d'une fiche et sa mise à jour par
son propriétaire. On ne fait pas payer une entreprise pour corriger ses
propres informations.

---

## 3. Les acteurs

| Qui | C'est qui | Ce qu'ils font |
|---|---|---|
| **Visiteurs** | Le grand public | Cherchent, consultent, nous signalent des erreurs |
| **Clients** | Dirigeants/employés d'entreprises | Revendiquent leur fiche, souscrivent la Mise en avant |
| **Annonceurs** | Entreprises qui achètent de la pub | Traitent avec un membre de l'équipe |
| **L'équipe (staff)** | Nous | Modération, données, ventes, support |

Côté staff, chaque compte a un rôle (`super_admin`, `support`,
`data_editor`, `ads_rep`). À ce stade, le rôle est **indicatif** — tout le
monde voit tout dans `/admin` ; il sert à savoir qui fait quoi, pas à
restreindre l'accès. Les comptes staff sont créés par un administrateur
(mot de passe + code TOTP obligatoire).

Les clients, eux, n'ont **pas de mot de passe** : ils se connectent avec leur
e-mail et un code à 6 chiffres reçu par courriel (valable 10 minutes). S'ils
disent ne rien recevoir : vérifier le dossier spam, attendre 60 secondes
avant de redemander un code.

---

## 4. Le workflow central : la réclamation de fiche

C'est le flux le plus important à maîtriser, car il conditionne tout le
reste : **seule une entreprise dont la fiche a été revendiquée et approuvée
peut acheter la Mise en avant.**

```
Client                          Équipe                        Résultat
──────                          ──────                        ────────
Trouve sa fiche
  → « Revendiquer »
  → se connecte (code e-mail)
  → remplit le formulaire :
    lien avec l'entreprise
    (propriétaire / gérant /
    employé / autre),
    téléphone, justification
                                Notification Telegram
                                (+ file /admin/claims)
                                Vérifie la demande
                                  → Approuver ou Refuser
                                  (bouton Telegram ou /admin)
                                                              E-mail automatique
                                                              envoyé au client
Si approuvée : la fiche lui appartient, badge « Vérifié »,
il peut la mettre en avant.
```

### Comment juger une réclamation

- **La justification doit être vérifiable** : un e-mail au domaine de
  l'entreprise, un numéro RCCM, un poste identifiable. « C'est ma société »
  sans rien d'autre ne suffit pas.
- En cas de doute, ne pas approuver : contacter le demandeur (son e-mail et
  son téléphone sont dans la demande) et demander un élément de plus. Le
  refus n'est pas définitif — le client peut redéposer.
- **Une fiche = un propriétaire.** Approuver une réclamation rejette
  implicitement toute prétention concurrente ; en cas de conflit entre deux
  demandeurs, escalader à un `super_admin` avant de trancher.
- Depuis Telegram, le refus part **sans note explicative**. Si le refus
  mérite une explication au client, le faire depuis `/admin/claims`, où l'on
  peut joindre une note (elle est incluse dans l'e-mail de décision).

---

## 5. Le travail au quotidien

### A. Le bot Telegram — notre salle de contrôle

Toute l'équipe est dans un **groupe Telegram privé** où le bot Congopro
poste en temps réel :

- 📋 les nouvelles réclamations — **avec boutons Approuver / Refuser** ;
- ✉️ les messages du formulaire de contact ;
- ⭐ les mises en avant activées, en retard de paiement, résiliées ;
- 🏢 les fiches nouvellement publiées ;
- ⚠️ les erreurs techniques de la plateforme ;
- 📊 chaque matin à 7 h, le bilan de la veille (fiches ajoutées,
  réclamations en attente, mises en avant actives, revenu mensuel récurrent).

Deux commandes utiles dans le groupe : **`/pending`** (la file des
réclamations en attente, avec boutons) et **`/stats`** (le bilan à la
demande).

**Avant de pouvoir agir depuis Telegram**, votre compte Telegram doit être
lié à votre compte staff : tapez sur n'importe quel bouton, le bot vous
répond avec votre identifiant numérique ; transmettez-le à un administrateur
qui fera la liaison. Sans liaison, les boutons ne font rien (et c'est
voulu) : chaque décision est enregistrée au nom de la personne qui l'a
prise.

Un double-appui sur un bouton est sans danger : la deuxième pression répond
« Déjà traitée ».

### B. Gérer les données entreprises (`/admin/companies`)

- Trois statuts : **Publiée** (visible), **Brouillon** (en préparation,
  invisible), **Litige** (contestée — gelée le temps de vérifier).
- Créer/modifier une fiche : formulaire complet (identité, contact, adresse,
  réseaux sociaux, position GPS). La publication d'une nouvelle fiche est
  annoncée dans le groupe Telegram.
- La recherche publique se met à jour **automatiquement quelques instants
  après** chaque enregistrement — pas d'action supplémentaire à faire.
- Règle d'or : ne jamais supprimer d'information vérifiable ; en cas de
  doute sur une fiche, la passer en Litige plutôt que de la modifier à
  l'aveugle.

### C. Vendre et gérer la publicité (`/admin/ads`)

1. La négociation se fait hors plateforme (prix, période, visuel/texte).
2. Créer la campagne : contenu, emplacement (accueil ou recherche),
   mots-clés le cas échéant, période, poids, **prix convenu et vendeur** —
   ces deux derniers alimentent le suivi des revenus.
3. Activer la campagne ; elle tourne toute seule sur sa période.
4. L'interrupteur général (réglages) coupe toute la diffusion en urgence.

### D. Suivre les revenus (`/admin/revenue`)

La page « Revenus » montre : le **MRR** (revenu mensuel récurrent des mises
en avant, chiffres tirés en direct de Stripe), les abonnements un par un
(entreprise, client, statut, montant, échéance), et le registre des ventes
publicitaires avec leur vendeur. Si Stripe est momentanément injoignable, la
page l'indique par un bandeau — les montants réapparaissent tout seuls.

### E. Le support client

Deux portes d'entrée, un seul réflexe : répondre vite et par écrit.

| Canal | Usage | Engagement |
|---|---|---|
| Formulaire `/contact` | Demandes posées, corrections de fiches | Réponse sous **48 h ouvrées** (nous répondons par e-mail) |
| Canal Telegram public | Assistance rapide, questions simples | Au fil de l'eau |

Cas fréquents et réponses :

- **« Je ne reçois pas le code de connexion »** → vérifier le spam ;
  attendre 60 s entre deux demandes ; vérifier que l'adresse saisie est la
  bonne.
- **« J'ai payé mais rien ne se passe »** → l'activation prend quelques
  secondes après le retour de la page Stripe ; si le badge n'apparaît pas,
  vérifier le statut de l'abonnement dans `/admin/revenue` avant toute autre
  chose.
- **« Ma fiche contient une erreur »** → si la personne représente
  l'entreprise, l'orienter vers la réclamation (elle pourra gérer sa fiche) ;
  sinon, corriger nous-mêmes après vérification.
- **Demande publicitaire** → transmettre à un `ads_rep`.

L'adresse e-mail de support n'est **jamais publiée** sur le site (protection
anti-spam) : c'est en répondant depuis la boîte que le client l'obtient.

---

## 6. Le rythme de la plateforme

| Quand | Quoi | Qui s'en soucie |
|---|---|---|
| En continu | Notifications Telegram (réclamations, contacts, paiements, erreurs) | Toute l'équipe |
| 3 h 15 | Sauvegarde quotidienne de la base (locale + copie hors-site chiffrée) | Automatique |
| 7 h 00 | Bilan quotidien dans le groupe Telegram | Toute l'équipe |
| 48 h ouvrées | Délai max de réponse aux messages de contact | Support |

Si le bot signale une **erreur technique** (⚠️) : ne pas paniquer, ne rien
« réparer » soi-même — prévenir un `super_admin`. Les erreurs répétées sont
automatiquement regroupées pour ne pas inonder le groupe.

---

## 7. Les mots de la maison

| Terme | Définition |
|---|---|
| **Fiche** | La page publique d'une entreprise |
| **Réclamation** (claim) | Demande d'un client pour devenir propriétaire de sa fiche |
| **Vérifié** | Badge d'une fiche dont la réclamation a été approuvée |
| **Mise en avant** | L'abonnement 15 $/mois : priorité dans les résultats + badge « Promu » |
| **Promu** | Badge d'une fiche avec mise en avant active |
| **MRR** | Revenu mensuel récurrent (somme des abonnements actifs) |
| **Campagne** | Une publicité vendue, avec sa période et son emplacement |
| **Espace Entreprise** | Le compte client (`/account`) |
| **Brouillon / Publiée / Litige** | Les trois statuts d'une fiche |
| **Digest** | Le bilan quotidien de 7 h dans le groupe Telegram |

---

## 8. Vos premiers jours — checklist

1. ☐ Recevoir votre compte staff d'un administrateur et configurer votre
   application TOTP (le QR code n'est montré qu'une fois).
2. ☐ Rejoindre le groupe Telegram de l'équipe.
3. ☐ Taper sur un bouton du bot, récupérer votre identifiant Telegram, le
   faire lier par un administrateur.
4. ☐ Parcourir `/admin` : tableau de bord, entreprises, réclamations,
   publicités, revenus.
5. ☐ Faire le parcours client en entier sur le site public : chercher,
   ouvrir une fiche, ouvrir `/contact`.
6. ☐ Traiter votre première réclamation en binôme avec un membre de
   l'équipe.
