# Oponente bot — IMPLEMENTADO

**Objetivo:** jogar sozinho. Você desafia o apelido `bot`; a partida acontece
normal pra você, e o bot faz jogadas de verdade contra você.

> **Status (2026-07-09): no ar.** As três peças estão feitas em
> `Backend/Bot.mar` (o `botMove` puro), `Backend/Players.mar` (o `bot`
> reservado, `botId = 0`) e `Backend/Games.mar` (auto-aceite + loop do turno).
> O botão "🤖 Treinar contra o bot" entrou na folha "Novo desafio"
> (`Frontend/Home.mar`). **Conta no placar** (decisão do Marcio): partida de
> bot credita a vitória/derrota do HUMANO; o bot não tem linha em `players`,
> então o registro dele é no-op. Verificado E2E via HTTP: o bot baixa
> criaturas na curva de mana, lança feitiços com alvo (`J i t`), troca no
> tabuleiro, bate na cara, encerra o turno e ganha; o loop sempre converge.

## A sacada: por que sai barato

O motor de regras (`Shared.applyMoveBy`) é a ÚNICA fonte de verdade e é
replayado igual pelo servidor e pelo cliente. Disso decorre quase tudo:

- **O bot é só uma função pura** "dado o estado, escolha uma jogada legal",
  reusando o mesmo motor. Nada de reescrever regras.
- **O bot joga no SERVIDOR** e as jogadas dele entram no mesmo log append-only.
  O cliente JÁ sabe animar "o rival jogou" (pelo poll → fila → animação). Então
  o bot é **invisível pro frontend**: zero mudança de cliente.
- **Autoritativo e determinístico**: toda jogada do bot passa por `applyMoveBy`
  (não tem como trapacear), e o replay continua estável.

## As três peças

### 1. Um oponente `bot` reservado
- Semear uma linha em `players` com apelido `bot` (um `userId` sentinela fixo,
  criado no boot).
- Bloquear humanos de pegar o apelido: no serviço de apelido, rejeitar `bot`
  (case-insensitive) — ao lado do `ApelidoEmUso`.
- `Backend.Players.findByNick "bot"` resolve pra esse jogador, então
  `desafiar "bot"` já funciona pelo caminho existente.

### 2. Auto-aceite no desafio (pula a etapa de aceitar)
- `desafiarImpl` trata o caso `oponente == bot`: em vez de criar `status =
  Desafio` com `deckB = ""`, cria a partida já **`Ativa`** com `deckB` = um deck
  escolhido (aleatório pelo seed entre os três) e `turnA = True` (você primeiro).
- Resultado: você cai na mesa, joga o 1º turno e encerra — sem "aguardando
  aceitar" (o bot já "aceitou"). O resto do fluxo (você joga primeiro) fica igual.

### 3. Turno do bot no servidor (o loop)
Em `jogarImpl`, depois de aplicar + persistir a SUA jogada: se é uma partida de
bot **e** virou a vez do bot (`turnA == False`, bot é o B), rode o turno dele:

```
loop:
  se a partida acabou (winner /= 0): pare
  mv = Bot.botMove estado          -- uma jogada
  aplique mv (applyMoveBy False)   -- valida e avança o estado
  anexe mv ao log
  se mv == "E": pare
```

Com um teto de iterações (ex. 40) por segurança. Persiste todas as jogadas do
bot num write só e retorna. No próximo poll/refetch, o seu cliente vê as
jogadas do bot no log e anima igualzinho às de um humano.

## A heurística `botMove` (v1: gulosa, ~uma tela de código)

`botMove : GState -> String`  — o bot é o B, então `me = st.b`, `foe = st.a`.

**O bot faz um turno COMPLETO, como um jogador:** baixa criaturas, lança
feitiços (com alvo), **ataca** com as criaturas prontas (em outras criaturas ou
na cara do herói) e encerra a vez — respeitando as mesmas regras do motor
(Ímpeto ataca no turno que entra, sem Ímpeto dorme um turno, Guardião obriga o
alvo, mana e limite de campo). A única coisa que ele NÃO faz é conceder — bot
joga até o fim. Como o loop chama `botMove` de novo a cada jogada, ele
naturalmente intercala: baixa, ataca, baixa de novo, até `"E"`.

Devolve UMA jogada; o loop chama de novo até vir `"E"`. Prioridade:

1. **Baixar/lançar** a melhor carta que dá pra pagar:
   - Criaturas: entre as pagáveis com slot livre no campo, a de maior
     `(atk + hp)`, desempate por custo. Joga `"J i -"`. (As battlecries de
     criatura — Inti `DanoHeroi`, Iara/Iemanjá `CurarHeroi` — resolvem sozinhas
     no motor ao baixar; o bot não precisa de lógica extra, só baixar a criatura
     na hora certa, ex. a que cura quando está ferido.)
   - Feitiços: **sim, o bot usa magia**, e cada efeito tem uma regra de alvo
     própria (ver "Feitiços" logo abaixo).
   - Regra simples: baixa primeiro a carta de maior valor de mesa.
2. **Atacar** — pra cada criatura pronta (respeitando a regra de Guardião do
   motor):
   - Se o foe tem Guardião: tem que bater nele; ataca se a troca não for péssima.
   - Senão: faz uma troca favorável (meu `atk >= hp` do alvo **e** `atk` do alvo
     `< meu hp`) se existir; senão vai na cara (`"A s h"`).
   - Ataca do atacante mais forte pro mais fraco.
3. **Encerrar** — quando não sobra jogada nem ataque que valha: `"E"`.

Cada sub-decisão só propõe jogadas LEGAIS (checa custo, espaço no campo,
Guardião, `ready`) — e `applyMoveBy` é a rede de segurança: se escapar um erro,
o pior caso é "o motor rejeita, o bot encerra o turno", nunca corromper a
partida.

```
botMove st =
    let me = st.b, foe = st.a in
    case bestDeploy me foe of
        Just mv -> mv
        Nothing ->
            case bestAttack me foe of
                Just mv -> mv
                Nothing -> "E"
```

### Feitiços (sim, o bot usa) — regra de alvo por efeito

O bot lê o `fx` da carta (`Shared.cardOf`) e escolhe o alvo. `fxNeedsTarget`
diz quais pedem alvo (`DanoAlvo`, `Adormecer` = um slot inimigo); o resto é
sem alvo (`"J i -"`).

- `DanoAlvo n` (Cipó-Estrangulador, Lança de Obsidiana): mira uma criatura
  inimiga que **morre** com o dano (`hp <= n`), preferindo a de maior `atk` ou
  um Guardião; se nenhuma morre, a maior ameaça. `"J i t"`.
- `Adormecer` (Canto da Iara): mira o atacante inimigo **pronto** mais forte,
  pra tirar o golpe dele do próximo turno. `"J i t"`.
- `DanoTodos n` (Fúria do Vulcão): AoE — lança quando o inimigo tem **duas ou
  mais** criaturas que ele fere de verdade (idealmente matando ≥2). Não
  desperdiça em campo vazio ou com 1 bicho grande. `"J i -"`.
- `CurarHeroi n` / `CurarComprar n m` (Garrafada do Pajé, Maré Cheia): lança
  quando a vida do bot está baixa (limiar ex. ≤ 12). `CurarComprar` também
  compra, então é um pouco mais ansioso. `"J i -"`.
- `Comprar n`: lança quando a mão está curta e sobra mana, por vantagem de
  cartas. `"J i -"`.

Como as magias-alvo só propõem alvos legais e `applyMoveBy` valida, o pior caso
continua sendo "o motor rejeita, o bot parte pro ataque / encerra".

## Descoberta — como o jogador acha o bot

O apelido `bot` não pode ser um segredo. **Decidido: um botão dedicado**
"⚔️ Treinar contra o bot" na folha **"Novo desafio"** (que já é UI nativa) — um
toque desafia o bot, sem digitar nem adivinhar a palavra. Vira o "modo solo" de
fato. `desafiar { nick = "bot", deck = <o deck já escolhido no seletor> }` reusa
o caminho existente; nem precisa do campo de apelido.

Detalhe: esse botão entra **junto** com o backend do bot — não antes, pra não
anunciar um bot que ainda não joga. É uma mudancinha em `Frontend/Home.mar` (na
folha do desafio). (Como reserva, uma linha de dica embaixo do campo — "Sem
rival por perto? Digite **bot**." — fica de brinde, mas o botão é o principal.)

## Níveis de dificuldade (depois, opcional)
- **Fácil**: uma jogada legal aleatória (divertido, fraco).
- **Médio (v1)**: a heurística gulosa acima.
- **Difícil**: lookahead de 1 lance — pra cada jogada candidata, `applyMoveBy`
  pra simular, pontua o estado resultante (diferença de vida, valor de mesa),
  escolhe a melhor. Barato, porque o motor é puro e rápido.
- Dá pra pendurar a dificuldade na própria partida (uma coluna, ou embutida no
  `deckB`).

## Determinismo e jogo limpo
- Toda aleatoriedade (deck do bot, desempates) sai do seed da partida pelo LCG
  que já existe → replays estáveis, cliente e servidor concordam.
- O bot **não tem informação escondida** por construção: `botMove` só lê `st.b`
  (o dele) + o BOARD/vida de `st.a` (público) — a mesma informação que um humano
  no lugar do B tem. É só não codar ele pra espiar `foe.hand`/`foe.deck`.
- Cada jogada do bot é logada + validada → autoritativo no servidor, sem
  trapaça, e a partida inteira continua reproduzível.

## Onde o código mora
- `Backend/Bot.mar` (novo): `botMove` + auxiliares (`bestDeploy`, `bestAttack`,
  pontuação). Só servidor; importa `Shared` pra `cardOf`/`sideOf`/`applyMoveBy`.
  O cliente nunca precisa dele (as jogadas do bot chegam pelo log).
- `Backend/Games.mar`: o loop do turno do bot em `jogarImpl` + o auto-aceite do
  bot em `desafiarImpl`.
- `Backend/Players.mar`: semear o usuário bot no boot + rejeitar o apelido
  reservado `bot`.

## Esforço
- Encanamento (apelido reservado + auto-aceite + loop): ~½ dia.
- `botMove` guloso: ~½ dia.
- Afinar sensação/dificuldade: aberto (mas um bot guloso já é jogável).

## Testes
- **Unit**: dar estados montados pra `botMove` e conferir jogadas sensatas
  (baixa a criatura pagável, respeita Guardião, encerra quando trava). Função
  pura → trivial de testar.
- **E2E**: desafiar `bot`, jogar, ver ele responder pelo mesmo pipeline de
  animação. O espelho Go/JS/python do motor mantém o determinismo honesto.

## Decisões (batidas)
1. Deck do bot: **aleatório por seed** (entre os três), replays estáveis. ✓
2. **Um bot só, guloso** (a heurística v1). Personalidades/dificuldades ficam
   pra depois (ver "Níveis de dificuldade"). ✓
3. Partidas de bot no lobby: **junto das outras** ("Partidas em andamento") —
   sem balde separado; o rival aparece como `bot`. ✓
4. **Conta no placar** (Marcio, 2026-07-09): a partida credita a v/d do humano;
   o bot não tem linha em `players`, então o `bumpRecord` dele é no-op. ✓
