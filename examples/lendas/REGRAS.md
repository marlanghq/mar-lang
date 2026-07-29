# LENDAS — Regras do Jogo

*Um jogo de cartas colecionável sobre os mitos da América Latina.
Dois jogadores, partidas assíncronas: jogue seu turno agora, seu rival
responde quando puder.*

## O básico

- Cada jogador começa com **20 de vida**. Quem zerar a vida do rival vence.
- Cada jogador escolhe **um deck pronto de 20 cartas** ao entrar na partida
  (o desafiante escolhe ao desafiar; o desafiado, ao aceitar).
- O **desafiante joga primeiro** e começa com **4 cartas** na mão; quem
  aceita o desafio compensa começando com **5 cartas**.

## Energia

A energia é o custo de tudo (o "mana" do jogo):

- No começo do **seu** turno, seu limite de energia sobe **+1** (máximo
  **8**) e seus cristais **recarregam por completo**.
- Cada carta mostra seu custo no cristal do canto. Você pode jogar
  quantas cartas a energia do turno pagar.

## O turno

No começo do seu turno, automaticamente: energia recarrega (+1 de
limite), suas criaturas **acordam** e você **compra 1 carta**. Depois,
em qualquer ordem:

1. **Jogar cartas** da mão (pagando energia).
2. **Atacar** com criaturas prontas.
3. **Encerrar o turno** — aí é a vez do rival.

- A mão comporta no máximo **8 cartas** — comprar com a mão cheia
  **queima** a carta comprada.
- Se o seu baralho acabar, cada compra vira **fadiga**: você perde 1 de
  vida, depois 2, depois 3…

## Criaturas

- Uma criatura tem **custo**, **ataque** e **vida**, e entra no seu campo
  (máximo **5 criaturas** em campo).
- Criatura recém-jogada entra **cansada**: só ataca no seu próximo turno.
  (Exceto com **Ímpeto** — veja habilidades.)
- **Atacar**: uma criatura pronta pode atacar **uma criatura inimiga** ou
  **o rival diretamente**.
  - Contra criatura: as duas se ferem **ao mesmo tempo** (cada uma sofre
    o ataque da outra). Criatura com vida ≤ 0 vai pro descarte.
  - Contra o rival: ele perde vida igual ao ataque.
- Cada criatura ataca **uma vez por turno**.

## Habilidades

- **Ímpeto** — pode atacar no turno em que entra.
- **Guardião** — enquanto houver um Guardião no campo inimigo, seus
  ataques **precisam mirar um Guardião** (ele protege o resto).
- **Ao entrar: …** — efeito automático quando a criatura entra em campo
  (causar dano, curar, comprar carta…).

## Feitiços

Feitiço é jogado da mão, faz o efeito impresso e vai pro descarte.
Alguns pedem **alvo** (uma criatura inimiga); outros agem sozinhos.

- **Adormecer** faz a criatura alvo perder o próximo ataque dela
  (ela acorda um turno depois das outras).

## Fim de jogo

- A partida termina quando um jogador chega a **0 de vida** (por ataque,
  efeito ou fadiga) — o outro vence — ou quando alguém **concede**.
- Vitórias e derrotas ficam no seu placar público.

## Os três decks

| Deck | Estilo | Tema |
|---|---|---|
| **Floresta** 🌿 | equilibrado, muralhas e sustento | folclore brasileiro: Saci, Curupira, Boitatá, Mapinguari… |
| **Sol** ☀️ | agressivo, dano direto | Andes e Mesoamérica: Guerreiro Jaguar, Inti, Quetzalcóatl… |
| **Águas** 🌊 | controle, cura e cartas | rios e mar: Iara, Boto, Iemanjá, Minhocão… |

Cada deck tem 10 cartas diferentes, duas cópias de cada. A ordem do
baralho é sorteada no início da partida.

## Etiqueta assíncrona

- A partida **fica salva no servidor**: feche o navegador, volte amanhã,
  o jogo continua de onde parou.
- Você pode ter **uma partida em andamento por rival** (desafios
  pendentes contam) — nada de spam de desafios na mesma pessoa.
- Quando for a vez do rival, a tela avisa e atualiza sozinha quando ele
  jogar (com direito a assistir as jogadas dele acontecendo).

---

*Nota técnica honesta: neste exemplo o cliente recebe a partida inteira
(sementes e baralho) para reproduzir o estado — um oponente curioso com
o DevTools aberto consegue espiar sua mão. Num jogo competitivo de
verdade, o servidor esconderia a informação privada; aqui o foco é o
motor de regras determinístico e o fluxo assíncrono.*
