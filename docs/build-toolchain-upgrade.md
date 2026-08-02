# План: обновление build-тулчейна и гигиена lock-файла

> **СТАТУС: реализовано (2026-07-26).** Шаги 1–4 и «смежная гигиена» выполнены; шаг 5
> сознательно не делался. Отклонения от исходного плана и то, что всплыло по ходу, —
> в разделе «Что получилось по факту» внизу.
>
> Держим отдельно от апгрейдов библиотек (React/TS/Lexical).

## Контекст / проблема

- `webapp/package.json`: большинство версий — диапазоны `^`/`~` (28 из 39 runtime, 53 из 64 dev).
  Реальная фиксация — это `package-lock.json`, а не сам `package.json`.
- `.nvmrc` = **20.11**, но dev-машина сейчас на **Node 24.18 / npm 11.16**. `npm install` под npm 11
  переписывает лок (lockfileVersion 2→3, перерешает зависимости) — именно это в прошлый раз дало
  дрейф (`lib0`, `isomorphic.js`, `dev`→`devOptional`). `npm ci` так не делает.
- `engines: {}` пуст, нет `.npmrc`, нет `packageManager` → тулчейн ничем не форсится.
- CI уже ставит через `npm ci` (хорошо), Node 20.11.
- Ограничение библиотек: Lexical запинен на 0.45.0 из-за React 17 + TS 4.6. **Апгрейд Node/npm
  ортогонален React/TS — их здесь не трогаем.**

## Цели

1. Установка зависимостей воспроизводима; регенерация лока — только осознанно и безопасно.
2. Единая актуальная версия Node/npm везде (dev, CI, `engines`).

## Шаги

### 1. Выбрать целевой тулчейн
- Node: **22 LTS** (или остаться на 20 LTS, если осторожно). Не пиннить Node 24 (не LTS).
- npm: штатный для выбранного Node (npm 10 для Node 22).
- Wails v2.13 / Go — отдельная тема, зафиксировать текущие версии, тут не менять.

### 2. Выровнять тулчейн везде
- `.nvmrc` → цель (напр. `22`).
- CI `dev-release.yml`/`prod-release.yml`: захардкоженный `node-version: 20.11.0` → `node-version-file: webapp/.nvmrc` (в `ci.yml` уже так).
- `webapp/package.json`: `engines: { "node": ">=22 <23", "npm": ">=10 <11" }`; опционально `"packageManager": "npm@10.x"` (Corepack).
- `webapp/.npmrc`: `engine-strict=true` — при несовпадении падать с ошибкой, а не молча переписывать лок.

### 3. Регенерировать лок — один раз, намеренно
- `nvm install && nvm use` (цель); `corepack enable`, если решили с `packageManager`.
- `rm -rf node_modules package-lock.json && npm install` → чистый lockfileVersion 3 на целевом npm.
- Ревью диффа: какие версии сдвинулись внутри диапазонов; проверить совместимость React 17 / TS 4.6 / Lexical 0.45.0 / webpack.
- Полная проверка: `npm run pack`, `npm run check`, `npm run test`, `npm run cypress:ci`;
  затем `make mac-dmg-wails` (embed-сборка); сервер не затрагивается.

### 4. Guardrails (защита от случайного дрейфа)
- `make prebuild`: `npm install` → **`npm ci`** (детерминированно, лок не мутирует; требует синхронности lock↔package.json).
- CI оставить на `npm ci` (уже так).
- Договориться: лок регенерим только осознанно и на запиненном тулчейне.

### 5. (Опционально, отдельно) массово запиннить точные версии в package.json
- Сейчас **не рекомендуется**: детерминизм даёт лок + `npm ci`. Вернуться, только если перестанем доверять локу.

## Смежная гигиена (можно сделать независимо): не коммитить `desktop/frontend/wailsjs`

- `desktop/frontend/wailsjs/` — генерируется Wails, webapp его не импортирует (зовёт `window.go.main.App.*`
  через инжектируемый рантайм), и для релиз-сборки **не нужен** (`wails build -tags "json1 sqlite3 frontend"
  -skipbindings` собирается без него — проверено). `wails dev` регенерит его локально.
- Действия:
  - `git rm -r --cached desktop/frontend/wailsjs`
  - в `desktop/.gitignore` добавить `frontend/wailsjs/` (и `frontend/dist/`)
  - добавить `desktop/frontend/.gitkeep` — сам каталог `frontend/` **обязан существовать** на чистом
    клоне, иначе `wails build` падает (`frontend directory ... does not exist` — проверено); содержимое не важно.

## Риски / заметки

- Не смешивать апгрейд Node/npm с бампом React/TS/Lexical — отдельные PR.
- `engine-strict` заблокирует установку на «не том» Node — это цель, но предупредить команду.

## Что получилось по факту

**Цель — Node 24, не 22.** На момент реализации Active LTS — уже Node 24 (`nvm ls`:
`lts/* -> krypton (v24.18.0)`), 22 ушёл в maintenance. Пиннили `24` / npm 11, совпадает
с dev-машиной. `engines: { node: ">=24 <25", npm: ">=11 <12" }`.

**Порядок шагов в release-workflow был сломан.** В `dev-release.yml`/`prod-release.yml`
`npm ci` стоял **до** `Setup Node`, т.е. ставился на дефолтном Node раннера и пин ни на что
не влиял. Переставили: Setup Node → npm ci, `node-version-file: focalboard/webapp/.nvmrc`.

**`@formatjs/ts-transformer` пришлось запиннить точно.** Диапазон `^3.9.2` на npm 11 резолвится
в 3.14.2, которому нужен peer `ts-jest@^29`, а проект на `ts-jest@27` → `ERESOLVE`, установка
падала. Запинили `3.9.9` — ровно то, что резолвил старый лок, т.е. фиксация текущего
состояния, а не бамп. `--legacy-peer-deps` сознательно не использовали.

**npm 11 блокирует install-скрипты зависимостей по умолчанию** (`allow-scripts`,
`npm approve-scripts`). Из 7 «pending» реально нужен только `cypress` (postinstall качает
бинарь раннера) — он в allowlist в `webapp/.npmrc`. Остальные (`@swc/core`, `core-js`,
`core-js-pure`, `gifsicle`, `cwebp-bin`, `@parcel/watcher`, `fsevents`) не нужны: pack/test/check
зелёные без них, нативщину закрывают prebuilt-пакеты из optionalDependencies.

**Дрейф лока:** lockfileVersion 2 → 3; 446 пакетов сменили версию, 113 добавлено, 32 удалено.
Критичные пины **не сдвинулись**: React 17, Lexical 0.45.0, jest 27, ts-jest 27.
Сдвиги в рамках мажоров: TS 4.7.4→4.9.5, webpack 5.73→5.109, sass 1.53→1.102,
eslint 8.19→8.57, @babel/core 7.18→7.29, react-select 5.8.0→5.10.2.

**51 снапшот в 14 сьютах обновлён.** Причина — только emotion-хеши CSS-классов react-select
(`css-1d1qzc4-MenuList` → `css-1fmksep`); проверено, что в диффах нет ни одной строки
без `css-<hash>`, т.е. разметка идентична.

**Проверено:** `npm run check`, `npm run test` (144/144 сьюта, 847 тестов), `npm run pack`,
`make mac-dmg-wails` — всё зелёное на дереве, поставленном через `npm ci`.
Отдельно подтверждено, что `npm ci` оставляет лок **байт-в-байт** неизменным, и что
`engine-strict` даёт `EBADENGINE` на несовпадающем Node.

**`npm run cypress:ci` не прогонялся:** Cypress 9.7.0 — x64-only, на Apple Silicon падает
с `Unknown system error -86`. Это предсуществующее ограничение, не регрессия апгрейда;
в GitHub Actions cypress не запускается ни в одном workflow.

**Документация:** `CLAUDE.md` и `docs/dev-tips.md` (там же убрано устаревшее «Node (v10+)»).

## Cypress на Apple Silicon — сделано отдельной веткой

Ветка `test/cypress-14` (от `build/toolchain-node24`, не от `main` — иначе конфликт лока),
коммит «Upgrade Cypress 9 -> 14 for native Apple Silicon support».

Причина, по которой E2E не запускались: **Cypress 9.7.0 не имеет darwin-arm64 сборки**,
x64-бинарь падает с `Unknown system error -86` без Rosetta 2 (на этой машине не установлена).
Нативный arm64 появился в **Cypress 10.2.0** ([cypress-io/cypress#19908](https://github.com/cypress-io/cypress/issues/19908),
закрыт ботом «Released in 10.2.0»); проверено по CDN: `10.1.0` → 404 для `arch=arm64`, `10.2.0` → 200.
Сейчас в кэше `Mach-O 64-bit executable arm64`, `cypress verify` проходит.

Сделано: Cypress 14.5.4 + миграция на v10-раскладку (`cypress.config.ts`, `cypress/e2e/`,
`support/e2e.ts`, `setupNodeEvents`), `@testing-library/cypress` → ^10, убран дубль `cypress`
из devDependencies (из-за него `npm ci --no-optional` в release-workflow всё равно его ставил).
Плюс два фикса: глобальный `window:before:load` вместо голого `localStorage.setItem()` (Cypress 12+
включает test isolation, и запись уходила не в тот origin — приложение не видело сессию),
и `'Create an empty board'` → `'Create empty board'` в `uiCreateEmptyBoard`.

**Стало 6 из 11 тестов (было 1 из 11).** Оставшиеся 5 падений — не последствия миграции,
а протухшие тесты: E2E не запускаются ни в одном workflow, поэтому дрейф UI никто не ловил.

Что чинить в follow-up:

| Спек | Тест | Симптом |
|---|---|---|
| `createBoard.ts:24` | MM-T4274 Create an Empty Board | не находит шаблон `'Meeting Agenda'` — имя устарело |
| `createBoard.ts:219` | GH-2520 cut/undo/redo в комментариях | `<div.MarkdownEditorInput>` пустой вместо `Test Text` |
| `manageGroups.ts` | MM-T4285 Adding group color | `cy.within()` получает 2 элемента: `.KanbanColumnHeader .Editable[value='New group']` |
| `manageGroups.ts` | MM-T4287 Hiding/unhiding a group | то же — селектор группы неоднозначен |
| `cardBadges.ts` | Shows and hides card badges | `<input.Editable.title>` не появляется |

Отдельно стоит решить, добавлять ли `cypress:ci` в `ci.yml` — без этого тесты протухнут снова
ровно так же. Сейчас **не добавлено**, т.к. 5 тестов красные.
