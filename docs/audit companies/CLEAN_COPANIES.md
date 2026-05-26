1. Fautes d'orthographe — champ
city
Valeur erronée	Valeur correcte	Occurrences	Gravité
Kisnhasa	Kinshasa	1	Erreur
KINSHASA	Kinshasa	1	Casse
Kinshasa (espace final)	Kinshasa	4	Espace
Lububmashi	Lubumbashi	1	Erreur
lubumbashi	Lubumbashi	2	Casse
Brazaville	Brazzaville	1	Erreur
BRAZZAVLLE	Brazzaville	1	Erreur + casse
Brazzaville (espace final)	Brazzaville	2	Espace
Pointe Noire	Pointe-Noire	4	Trait d'union
Pointe-noire	Pointe-Noire	1	Casse
Pointe noire	Pointe-Noire	1	Casse + tiret
Pointe Noire (espace final)	Pointe-Noire	2	Espace + tiret
Point-Noire	Pointe-Noire	1	Erreur
Kolwezi (espace final)	Kolwezi	2	Espace
Butembo (espace final)	Butembo	1	Espace
En tout, Pointe-Noire compte 7 variantes distinctes pour 90 entreprises. C'est le champ le plus dégradé.

2. Confusion Ville / Commune
Problème	Exemples	Nb
Commune de Kinshasa utilisée dans city au lieu de Kinshasa	Ngaliema, Masina, Barumbu, Lingwala, Maluku, Gombe	8
address_line_2 utilisé comme champ commune (parfois préfixé « Commune de »)	Gombe (143×), Commune de la Gombe (35×), Limete (18×), Ngaliema (13×)	~300
address_line_2 utilisé comme boîte postale (BP)	BP. 1588, BP. 8834, BP 2484…	9
Valeur trop longue dans city (phrase entière)	Brazzaville et Pointe-Noire, République du Congo	1

3. Regexp

"city":\s*"(?!(Kinshasa|Lubumbashi|Brazzaville|Matadi|Pointe|Mbuji|Kolwezi|BRAZZAVILLE|Ngaliema|Goma|Boma|Muanda|Likasi|Bukavu|Mbanza|N'Kayi|Kikwit|Kananga|Mbandaka|Kisangani|Butembo|Lusaka|Masina|Barumbu|Lingwala|Maluku|BRAZZAVLLE)).*