# План: избавиться от npm-ворнингов и увязать это с React 19 / Solid

> Держим отдельно от `docs/build-toolchain-upgrade.md` (сделано: Node 24 / npm 11 / `npm ci` /
> Vite). Этот документ — про **содержимое** дерева зависимостей, а не про тулчейн, который его ставит.
>
> **СТАТУС: эшелоны 0 и 1 сделаны (2026-08-01)**, кроме одного пункта —
> `react-beautiful-dnd` → `@dnd-kit` перенесён в эшелон 2, причина в «Что получилось по факту».
> Эшелон 2 — впереди.

## Метрика

Единственное честное измерение — **чистая** установка (`rm -rf node_modules package-lock.json`),
а не `npm ci` поверх готового дерева: последний просто воспроизводит лок и половину ворнингов не печатает.

| | до | эшелон 0 | эшелон 1 | пол |
|---|---|---|---|---|
| ERESOLVE | 1 | 0 | **0** | 0 |
| строк `npm warn deprecated` | 21 | 12 | **3** | 2 |
| уязвимости | 53 (1 critical, 44 high) | 16 (3 high) | **0** | 0 |
| пакетов в дереве | 1271 | 1106 | **1081** | — |
| install-скриптов в `allow-scripts pending` | 6 | 5 | **4** | 4 |

«Пол» — то, что не убирается ничем: 4 install-скрипта реальных build-инструментов (см.
«Что останется») и 2 deprecated, которые пинят **сами пакеты jest 30**:
`@jest/reporters`/`jest-config`/`jest-runtime` → `glob ^10.5.0` и
`jest-environment-jsdom` → `jsdom ^26.1.0` → `whatwg-encoding`. Третья строка —
`react-beautiful-dnd`, она уйдёт вместе с ним в эшелоне 2.

## Контекст

Чистая установка (`npm ci` на пустом `node_modules`) сейчас даёт:

- **1 ERESOLVE** — `@lexical/react@0.45.0` тянет `react-error-boundary@6`, которому нужен React 18/19,
  а мы на React 17;
- **21 строку `npm warn deprecated`**;
- **6 пакетов в `allow-scripts pending`** (npm 11 блокирует install-скрипты по умолчанию);
- **53 уязвимости** по `npm audit`: 4 low, 4 moderate, 44 high, **1 critical**;
- 2 служебных ворнинга от git-зависимости (`gitignore-fallback`, `skipping integrity check`).

Почти всё это — не «плохие библиотеки», а **три застрявшие версии**: `jest@27`, `eslint@8` и пин
`react@17.0.2`. Плюс горсть пакетов, которые просто забыли удалить.

## Инвентаризация: откуда что берётся

Каждая строка проверена через `npm ls <pkg> --all`.

| Ворнинг | Реальный источник | Эшелон |
|---|---|---|
| ERESOLVE `react-error-boundary@6` | `@lexical/react@0.45.0` × пин React 17 | 0 (override) или 2 |
| `fstream`, → `rimraf@2` → `glob@7` → `inflight` | **прямая зависимость, в коде не используется** | 0 |
| `mini-create-react-context` | **прямая, не используется** | 0 |
| `@types/react-intl` (stub) | **прямая, не используется** | 0 |
| `fetch-mock-jest`, → `lodash.isequal`, `querystring`, `core-js`, `path-to-regexp` (high) | **прямая, не используется** — `src/test/fetchMock.ts` написан руками | 0 |
| critical «Malware in eslint-config-mattermost» | `eslint-plugin-mattermost` (git-пин) — в его `package.json` **чужое имя** `eslint-config-mattermost`, npm матчит его с advisory на сквоттер в реестре. Ложное срабатывание, но красное | 0 |
| `gitignore-fallback`, `skipping integrity check` | тот же git-пин | 0 |
| `abab`, `domexception`, `w3c-hr-time`, `whatwg-encoding` | `jsdom@16` ← `jest@27` | 1 |
| `glob@7`, `rimraf@3`, `minimatch`/`brace-expansion` (high) | `jest@27` + `eslint@8` + `ts-jest@27` | 1 |
| `eslint@8.57.1` («no longer supported»), `@humanwhocodes/*` | `eslint@8` | 1 |
| high в `stylelint`/`stylelint-order`/`stylelint-scss` | `stylelint@14` → `flat-cache` → `rimraf@3` | 1 |
| `uuid@8`, `qs` (moderate) | `cypress@14` → `@cypress/request` | 1 |
| `axios` (high) | `start-server-and-test@1` → `wait-on` | 1 |
| `core-js-pure` (install-скрипт) | `@testing-library/react@11` → `@testing-library/dom@7` → `aria-query@4` | 1 |
| `ts-jest@27` (high; **в jest-конфиге не используется**, transform = `@swc/jest`) | держится peer-ом от `@formatjs/cli@4.8.4` → nested `@formatjs/ts-transformer@3.9.4` | 1 |
| `react-beautiful-dnd` (deprecated Atlassian) | прямая, 4 файла сайдбара | 1 (не требует React 18!) |
| `react-redux@7.2.4`, `react-intl@5`, `react-router-dom@5`, `react-day-picker@7`, `@fullcalendar@5`, `@testing-library/react@12`, `typescript@4.9` | упираются в пин `react@17.0.2` | 2 |

`react-dnd@14` (канбан, таблица, blocksEditor — 10 файлов) **не deprecated**, просто старый. Он не
источник ни одного ворнинга.

## Эшелон 0 — чистка, ни от чего не зависит

Ни один пункт не трогает ни React, ни тулчейн. Это удаление мусора и один override.

1. **Удалить 4 неиспользуемые зависимости**: `fstream`, `mini-create-react-context`,
   `react-hot-keys` (используется `react-hotkeys-hook`), `trim-newlines`, плюс dev-зависимость
   `fetch-mock-jest`. Проверено grep-ом по `src/` и `cypress/` — 0 импортов.
2. **Удалить stub-типы**: `@types/react-intl` (deprecated стаб), `@types/react-select` (стаб —
   react-select 5 везёт свои типы), `@types/nanoevents` (nanoevents 5 везёт `index.d.ts`).
3. **Убрать git-зависимость `eslint-plugin-mattermost`.** Это 7 файлов без зависимостей: `index.js`
   и два конфига (`configs/.eslintrc.json`, `configs/.eslintrc-react.json`), из которых мы используем
   только `plugin:mattermost/react`. Заваендорить оба JSON в `webapp/.eslintrc/` и заменить `extends`
   на относительные пути. Убирает **critical-алерт**, оба служебных ворнинга и внешнюю зависимость
   от чужого git-репозитория в сборке.
4. **`overrides: {"react-error-boundary": "^4.1.2"}`** в `webapp/package.json`. v4 объявляет peer
   `react >=16.13.1`, а `LexicalErrorBoundary` использует ровно `<ErrorBoundary fallback={…}
   onError={…}>` — API, который в v4 есть. Убирает единственный ERESOLVE, не трогая пин React.
   Если тест на markdown-редактор покраснеет — откатить и оставить ворнинг до эшелона 2.

**Убирает:** critical-алерт, ERESOLVE, ~8 из 21 deprecated-строк, часть high (`path-to-regexp`),
1 из 6 install-скриптов (`core-js`). **Оценка: 0.5–1 день.**

## Эшелон 1 — dev-тулчейн, React не трогаем

Здесь всё независимо друг от друга, можно резать на отдельные PR-ы.

1. **`jest@27` → `jest@30`** (+ `jest-mock@30`, `@types/jest@30`, явный `jest-environment-jsdom@30`,
   который с jest 28 — отдельный пакет). `@swc/jest` peer-ит только `@swc/core`, так что transform
   переживёт. Убирает `jsdom@16` со всем выводком (`abab`, `domexception`, `w3c-hr-time`,
   `whatwg-encoding`) и `glob@7`/`rimraf@3` из своей ветки. **Цена: 126 снапшотов** — с jest 29
   поменялся дефолтный `snapshotFormat` (без `Object {` / `Array [`). Либо разово пересоздать, либо
   оставить `snapshotFormat: {escapeString: true, printBasicPrototype: true}` и не трогать. 152 сьюта,
   847 тестов — прогон обязателен.
2. **Удалить `ts-jest`** — в `jest.transform` его нет, transform = `@swc/jest`. Сначала
   **`@formatjs/cli@4.8.4` → `6.x`** (у 6.x нет зависимостей вообще, `engines: node >=20.12` — мы на 24),
   иначе его nested `ts-transformer@3.9.4` peer-ит `ts-jest@27` и npm поставит его обратно.
   Проверить, что `npm run i18n-extract` даёт байт-в-байт тот же `i18n/en.json`.
3. **`eslint@8` → `eslint@9`** + flat config (`eslint.config.js`) + `@typescript-eslint@8`,
   `eslint-plugin-react@7.37`, `eslint-plugin-import@2.32`, `eslint-plugin-cypress@6`,
   `eslint-plugin-no-only-tests@3`. Заваендоренные в эшелоне 0 конфиги придётся переписать в flat-формат
   (это как раз аргумент вендорить их, а не ждать апстрим). Самый трудоёмкий пункт эшелона.
   Убирает `@humanwhocodes/*`, `eslint@8` deprecated, и `minimatch`/`brace-expansion` high из ветки eslint.
4. **`stylelint@14` → `17`** + `stylelint-config-sass-guidelines@13`. Убирает `flat-cache`→`rimraf`→`glob`.
   Стилистические правила в stylelint 15 выпилены — но SCSS у нас и так форматирует prettier (`fix:scss`).
5. **`cypress@14` → `15`** (закрывает `qs`, `uuid@8`) и **`start-server-and-test@1` → `3`**
   (закрывает `axios` high). Оба тривиальны; напомню, что 5 из 11 E2E сейчас красные
   (см. хвост `build-toolchain-upgrade.md`) — это отдельная история.
6. **`@testing-library/react@11` → `12.1.5`** — последняя версия с peer `react <18`. Тянет
   `@testing-library/dom@^8` (у нас уже 8.20.1 в корне) вместо nested `dom@7`, чем убирает
   `core-js-pure` и его install-скрипт.
7. **`react-beautiful-dnd` → `@dnd-kit/core` + `@dnd-kit/sortable`** для сайдбара. `@dnd-kit` peer-ит
   `react >=16.8`, то есть **это можно сделать на React 17**. Затрагивает 4 файла
   (`sidebar.tsx`, `sidebarCategory.tsx`, `sidebarBoardItem.tsx`, `testUtils.tsx`). Убирает
   единственный *deprecated* DnD-пакет; `react-dnd@14` (канбан/таблица) остаётся до эшелона 2.

**Убирает:** остальные deprecated-строки кроме React-зависимых, подавляющее большинство из 44 high,
ещё один install-скрипт. **Оценка: 4–7 дней**, из них eslint 9 — половина.

## Эшелон 2 — упирается в пин React 17

Тут выбор между двумя существующими документами. Сами по себе ворнинги здесь уже почти кончились —
остаётся ERESOLVE (если override из эшелона 0 не взлетел) и «старьё без deprecated-метки»:
`react-redux@7`, `react-intl@5`, `react-router-dom@5`, `react-day-picker@7`, `@fullcalendar@5`,
`typescript@4.9`, `react-dnd@14`, `@testing-library/*`.

## Что останется в любом случае

- **`allow-scripts pending`: 4 пакета** (`@swc/core`, `esbuild`, `@parcel/watcher`, `fsevents`) —
  это реальные build-инструменты (Vite, sass, swc). У npm 11 нет «списка отказа»: ворнинг
  снимается только внесением в allowlist, а мы сознательно скрипты не запускаем (prebuilt-пакеты
  из `optionalDependencies` их закрывают). Ворнинг — цена этого решения, не проблема.
  `core-js` уходит в эшелоне 0, `core-js-pure` — в эшелоне 1.
- `npm warn` про funding — косметика.

## Как это ложится на существующие планы в `docs/`

**Оба документа частично устарели: они написаны до перехода на Vite.**

- `react-19-compiler-migration.md` говорит про webpack, `ts-loader` и `react-compiler-webpack`.
  Актуально — `babel-plugin-react-compiler` через `babel.plugins` у `@vitejs/plugin-react`
  (он уже стоит, 5.2.0). Раздел «React Compiler» надо переписать под Vite.
- `solidjs-migration-plan.md` пункт 2 («настроить Vite») — **уже оплачен**, это самый дешёвый
  из его пунктов, и он пропал из аргументации в пользу Solid.
- Оба документа пересекаются с эшелоном 1: React-19-план перечисляет обновления Jest/RTL/TS
  внутри своего единого PR-а, Solid-план — «перенести 144 Jest-сьюта». Эшелоны 0 и 1 надо
  **вычесть из обоих** и сделать до них: они одинаково нужны при любом исходе, и делать их
  внутри 21-34-дневного PR-а значит смешивать «убрали мусор» с «поменяли модель рендеринга».
- React-19-план уже содержит ровно те бампы, которые закрывают эшелон 2 (Redux 9, Intl 7,
  FullCalendar 6, DnD, DayPicker 10, TS 5.9) и явно ставит критерий приёмки
  «`npm ci` и `npm ls` без peer warnings» — то есть **это и есть план по ворнингам эшелона 2**.

## Рекомендация

1. **Сделать эшелоны 0 и 1 отдельными PR-ами, сейчас, вне зависимости от выбора фреймворка.**
   Это ~5–8 дней, и они закрывают критикал, ERESOLVE, все deprecated-строки кроме React-зависимых
   и почти все high. Ни один час не пропадает при любом дальнейшем решении.
2. **Дальше — React 19, а не Solid**, если цель именно гигиена зависимостей:
   - React-19-план уже перечисляет нужные бампы и делает ворнинги критерием приёмки;
   - Solid убирает ворнинги *удалением* React-стека, но взамен вводит свой, более молодой и тонкий
     (`@dnd-kit/solid`, `@thisbeyond/solid-select`, **самописный** Lexical-primitive вместо
     `@lexical/react`, **самописные** `IntlProvider`/`useIntl` вместо `react-intl`). Это обмен
     известного списка ворнингов на неизвестный объём поддержки;
   - масштаб несопоставим: React 19 — это апгрейд 542 файлов, Solid — переписывание 542 файлов
     и 152 тестовых сьютов, при заморозке фич на весь период;
   - главный дешёвый выигрыш Solid-плана (Vite) уже получен отдельно.
3. **Разрезать React-19-план надвое.** Сейчас это один PR на 21–34 дня, где апгрейд React
   и включение React Compiler с вычищением 99 `React.memo` / 70 `useMemo` / 259 `useCallback`
   идут вместе. Предлагаю:
   - **PR A: React 18.3 warning-аудит → React 19 + peer-бампы + TS 5.9.** Закрывает эшелон 2.
     Модель рендеринга не меняется, мемоизация остаётся как есть.
   - **PR B: React Compiler + удаление ручной мемоизации + `eslint-plugin-react-hooks`.**
     Отдельный риск, отдельное профилирование, откатывается отдельно.

   Solid при этом не отменяется — он остаётся продуктовой ставкой на bundle size и перформанс
   (его собственные gates: −20% gzip, −15% time-to-interactive), а не способом починить `npm audit`.
   Оценивать его надо по этим числам, и уже после эшелонов 0–1, когда baseline чистый.

## Что получилось по факту: эшелон 0

Сделано всё, что перечислено выше, плюс **один незапланированный пункт**.

**Пришлось втянуть пункт 2 эшелона 1 (`@formatjs/cli` + `ts-jest`).** Полная регенерация лока
подняла `@formatjs/unplugin` 1.2.3 → 1.2.4 внутри диапазона `^1.2.3`, а вместе с ним
`@formatjs/ts-transformer` 4.4.17 → 4.4.18. Его peer `ts-jest@29` объявлен `optional`, но npm 11
всё равно поставил его вложенно — и притащил **целое дерево jest 30**: вложенных пакетов под
`@formatjs/unplugin` стало 7 → 71, всего в дереве 1389 против 1271. Оставлять эшелон 0 в состоянии
«ворнингов меньше, пакетов на 118 больше» было нельзя, поэтому сразу:
`@formatjs/cli` 4.8.4 → 6.16.15 (у 6.x вообще нет зависимостей, он bundled) и удаление `ts-jest`
из devDependencies — он не использовался с переезда на `@swc/jest`, держался только этими peer-ами.
Заодно удалён мёртвый блок `jest.globals["ts-jest"]`.

`npm run i18n-extract` на formatjs 6 даёт **тот же `i18n/en.json`**, единственная разница —
добавился перевод строки в конце файла (чего и требует наше же правило `eol-last`).
Там же `npx rimraf i18n/tmp.json` заменён на `node -e "…rmSync…"`: `rimraf` не был объявлен
ни в каких зависимостях, то есть `npx` тянул его из сети на каждый прогон.

**Override `react-error-boundary` не применился с первого раза.** `npm install` поверх готового
лока оставил вложенный `react-error-boundary@6.1.2` на месте, при этом `npm ls` уже показывал его
как `invalid: "^4.1.2"`, а в самом локе поля `overrides` не появилось. Лечится только полной
регенерацией (`rm -rf node_modules package-lock.json`). После неё стоит 4.1.2 и ERESOLVE исчез;
152 сьюта, включая `markdownEditorInput`, зелёные.

**Вендоринг eslint-конфигов.** `.eslint/mattermost-base.json` и `.eslint/mattermost-react.json` —
копии `configs/*` из `eslint-plugin-mattermost@23abcf99`. В base убрана строка
`"parser": "babel-eslint"`: корневой `.eslintrc.json` всё равно переопределяет parser на
`@typescript-eslint/parser`, а сам `babel-eslint` не был установлен вообще. Проверено через
`eslint --print-config src/app.tsx`: 355 правил, все семейства на месте (`header/header`,
`react/*`, `jquery/*`, `cypress/*`, `@typescript-eslint/*`). Побочный эффект: CI больше не клонирует
git-репозиторий на каждой установке.

**Заодно удалены** `@types/nanoevents@1.0.0` — типы для nanoevents **1.x** (`export = NanoEvents`),
тогда как у нас nanoevents 5 со своим `index.d.ts` и `createNanoEvents`; пакет был не просто
лишним, а описывал чужой API.

**Что осталось после эшелона 0** — ровно три кластера, все адресуются эшелоном 1:
`jest@27` → jsdom 16 (9 low + 4 deprecated), `cypress@14` → `@cypress/request` (3 moderate),
`start-server-and-test@1` → `wait-on` → `axios` (2 high), плюс `eslint@8` и `react-beautiful-dnd`.

## Что получилось по факту: эшелон 1

Сделаны шесть пунктов из семи. **0 уязвимостей**, deprecated 12 → 5.

**jest 27 → 30** (+ `jest-environment-jsdom`, отдельный пакет с jest 28; `@types/jest` 30;
`@testing-library/jest-dom` 5 → `~6.9.1`). Версия jest-dom подобрана, а не взята последняя:
в 6.10.0 появился peer `@testing-library/dom >=10`, а React 17 держит RTL 12, который тянет
`dom@8`. `jest-dom@7` требует того же. Три вещи сломались, все — среда, не продукт:

- **321 вызов удалённых алиасов** `toBeCalled`/`toBeCalledTimes`/`toBeCalledWith` в 71 файле
  (jest 30 оставил только `toHaveBeenCalled…`). Механическая замена.
- **`target.scrollIntoView is not a function`** в 30 тестах. jsdom не реализует scrollIntoView
  ни в одной версии; jsdom 26 просто стал доставлять focus, за который держится `onFocus`
  в `rootInput.tsx`. Полифилл-заглушка в новом `src/test/jsdomPolyfills.ts` (`setupFiles`).
- **87 снапшотов.** Три причины, ни одной продуктовой: дефолт `snapshotFormat` из jest 29
  (`Object {`/`Array [` больше не печатаются, строки не экранируются — задело всего 4 файла
  из 126), более полная сериализация CSS в jsdom 26 (`color: inherit`, `rgb(var(--…))`,
  `height: 0px` раньше молча терялись) и тот же обретённый focus: react-select теперь рисует
  свой live-region («Select is focused, type to refine list»), которого в jsdom 16 не было.

**`jest.resolver.js` удалён.** Он существовал ровно потому, что резолвер jest 27 не понимал
`exports` и не находил подпути Lexical. jest 30 понимает — проверено прогоном всех 152 сьютов
без него.

**RTL 11 → 12.1.5** (последняя с peer `react <18`). Один снапшот изменился по делу:
после клика по пункту меню оно теперь **закрывается**. Это и есть поведение `MenuWrapper`
(он слушает `menuItemClicked` на document), просто под RTL 11 обновление состояния не флашилось —
старый снапшот фиксировал состояние, которого в браузере не бывает.

**cypress 14 → 15, start-server-and-test 1 → 3.** Закрыли последние high (`axios` через `wait-on`)
и moderate (`qs`, `uuid@8` через `@cypress/request`). `cypress verify` проходит, бинарь arm64.

**stylelint 14 → 17 + sass-guidelines 9 → 13.** Стилистические правила уехали в
`@stylistic/stylelint-plugin`, `stylelint-order` из конфига исчез: в `.stylelintrc`
`indentation` → `@stylistic/indentation` (4, как в `.prettierrc.json`), а мёртвый
`order/properties-alphabetical-order` убран. Одна настоящая находка от stylelint-scss 7 —
`map-get` в `_z-index.scss`, переведён на `@use 'sass:map'` + `map.get`.

**eslint 8 → 9 + flat config** (`eslint.config.mjs`, `.eslintrc.json` и `.eslintignore` удалены,
`--ext` из скриптов убран — его в eslint 9 нет). Вместе с ним `@typescript-eslint` 5 → 8,
`eslint-plugin-react` → 7.37, `import` → 2.32, `cypress` → 6, `no-only-tests` → 3, плюс новые
`@eslint/js`, `globals` и `@stylistic/eslint-plugin` (typescript-eslint 8 отдал ему `indent`,
`semi`, `member-delimiter-style`, `type-annotation-spacing`).

Не eslint 10: `eslint-plugin-react` его ещё не поддерживает, а главное — eslint 10 удаляет
стилистические core-правила, на которых стоит вендоренный конфиг. Это отдельная работа.

Удалены два плагина: `eslint-plugin-jquery` (jQuery в коде нет вообще — 58 правил впустую)
и `eslint-plugin-babel` (единственное правило `babel/no-unused-expressions` заменено на
`@typescript-eslint/no-unused-expressions` с теми же опциями). `eslint-plugin-header` оставлен,
но его устаревшая `meta.schema` отключается в конфиге — иначе eslint 9 отвергает опции правила,
и проверка лицензионных заголовков пропала бы.

Паритет проверен машинно: `eslint --print-config src/app.tsx` до и после. Из активных правил
исчезли только jquery-семейство, `babel/no-unused-expressions` и две ts-нормы, которые
typescript-eslint 6 перенёс из `recommended` в `stylistic` (`adjacent-overload-signatures`,
`no-inferrable-types`) — эти две возвращены явно. Две смены строгости сохранены осознанно:
`@typescript-eslint/no-explicit-any` возвращён в `warn` (в v6 он стал `error`, а `--quiet`
означает, что 151 существующее место никогда не было гейтом) и
`no-unused-vars` получил `caughtErrors: 'none'` (дефолт до v8).

Настоящих находок от новых правил три, все исправлены: два рантайм-массива, существовавших
только чтобы вывести union-тип (`boardTypes` в `blocks/board.ts`, `channelTypes` в
`store/channels.ts`) — union теперь пишется напрямую, мёртвый код из бандла ушёл — и три строки
с табами вместо пробелов в `live-markdown-plugin`.

Единственное, что **выключено**, — новое `cypress/unsafe-to-chain-command` (в 2.x его не было):
30 находок, все `.type().type().should()`, и все в E2E, который сейчас 5/11 красный и не гоняется
ни в одном workflow. Чинить его надо там, где есть зелёный прогон для сверки, — в follow-up из
`build-toolchain-upgrade.md`, а не внутри апгрейда линтера.

### Два оставшихся deprecated: что с ними можно и чего нельзя

Ни один из них не используется нашим кодом — оба лежат глубоко внутри jest.

**`inflight` — убран.** Цепочка была `jest → @jest/transform → babel-plugin-istanbul@7 →
test-exclude@6 → glob@7 → inflight`. У `babel-plugin-istanbul` есть 8.0.0, где `test-exclude@7`
и glob без `inflight`, а разница мажоров — только `engines` (`node >=12` → `>=18`). Один
`overrides` обрубает цепочку и уносит заодно `glob@7`: 5 строк deprecated → 3, дерево 1089 → 1081.
Покрытие после подмены считается ровно то же (64.79/54.52/55.71/64.71), 152 сьюта зелёные —
а инструментация покрытия как раз и есть то, для чего этот плагин в дереве.

**`whatwg-encoding` — оставлен сознательно.** 3.1.1 это **последняя** версия: пакет депрекейтнут
целиком, в пользу `@exodus/bytes`, то есть это не «обновись», а «мигрируй». Мигрировать должен
jsdom, и он уже это сделал — jsdom 30 ходит через `html-encoding-sniffer@6 → @exodus/bytes`.
Но `jest-environment-jsdom@30.4.1` объявляет `jsdom ^26.1.0`, так что взять 30 можно только
override-ом.

Эксперимент проведён: с `jsdom@30.0.1` установка чистая, 0 уязвимостей, **34 теста падают
на 43 снапшотах — и все 43 это одна строка** `style` у инпута react-select
(`background: 0px` → `0px center`, добавился `font: inherit`, `outline: 0` → `0px`).
Разметка не меняется нигде, то есть обновить снапшоты было бы безопасно.

Откачено всё равно, и причина не в снапшотах: держать jsdom на четыре мажора выше того,
что объявляет собственный jest-овский environment, значит гонять комбинацию, которую вверху
никто не тестирует, — ради строки, которая не уязвимость, а уведомление о миграции. Уйдёт сама,
когда jest поднимет свой jsdom. То же рассуждение про `glob@10`: его пинят `^10.5.0` три пакета
самого jest 30.

### Что перенесено в эшелон 2: `react-beautiful-dnd` → `@dnd-kit`

План оценивал это как «4 файла», и по числу файлов так и есть. По существу — нет: сайдбар это
**вложенный сортируемый список с переносом между контейнерами** (категория одновременно
`Draggable` и `Droppable`, доски таскаются внутри и между категориями), ~1285 строк, где
render-prop API вплетён в условия рендера в шести местах (`snapshot.isDragging`,
`categorySnapshot.isDraggingOver`, `provided.placeholder`). В `@dnd-kit` это другая модель —
sensors, `DragOverlay`, collision detection, ручная логика смены контейнера в `onDragOver`.

Отложено не из-за объёма, а из-за **проверяемости**: 152 сьюта — снапшотные, они ловят
регрессии рендера и ничего не говорят о самом перетаскивании, а E2E по сайдбару не запускается.
Остальные шесть пунктов эшелона были заменами версий, проверенными существующим зелёным прогоном;
этот — переписывание работающей фичи вслепую.

Отложить дёшево: `react-beautiful-dnd@13.1.1` на React 17 работает и уязвимостей не имеет,
цена ожидания — одна строка `npm warn deprecated`. Форсирует его не гигиена, а React 19
(rbd не живёт со StrictMode) — то есть эшелон 2, где `docs/react-19-compiler-migration.md`
и так планирует заменить **весь** DnD разом (rbd + react-dnd + react-dnd-scrolling) и добавить
DnD-тесты на pointer/touch/keyboard во всех трёх зонах. Там это делается один раз и с сеткой,
а не дважды и без неё.

## Проверка

После **каждого** PR-а, на дереве, поставленном через `npm ci`:

- `npm ci` на пустом `node_modules` — записать число строк `npm warn deprecated`, `npm audit`
  (по severity) и наличие ERESOLVE; это и есть метрика прогресса;
- `npm ls` — без `invalid`/`extraneous`;
- `npm run check`, `npm run test` (152 сьюта), `npm run pack`;
- `make mac-dmg-wails` — embed-сборка десктопа (webapp попадает в бинарь через `desktop/pack`);
- после эшелона 0: точечно `npm run test -- -t markdownEditorInput` — единственное место, где
  override `react-error-boundary` может проявиться;
- `npm ci` не должен менять `package-lock.json` ни на байт (проверять `git diff --exit-code`).
