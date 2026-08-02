# Переход Focalboard с React на SolidJS

## Кратко

Переписать `webapp` на SolidJS одним релизом с полным функциональным и визуальным
паритетом. Одновременно заменить Redux на `solid-js/store` и Webpack на Vite, но
сохранить Jest. Серверные API, URL, SCSS, формат `pack/` и интеграции с
Go-сервером/Wails не менять.

Миграция ведётся в отдельной ветке без React/Solid runtime-bridge; предыдущий
React-релиз остаётся готовым артефактом для отката.

## Архитектура и интерфейсы

- Создать `createAppStore(deps): {state, actions}` на `createStore`.
  - Сохранить текущую форму `RootState`, чтобы упростить перенос 17 Redux-доменов.
  - Каждый домен экспортирует тип состояния, чистые selectors и
    синхронные/асинхронные actions.
  - Удалить Redux actions, reducers, thunks, `dispatch`, `react-redux` и Redux
    mock stores.
  - Обновлять коллекции через `reconcile` с ключом `id`, связанные изменения от
    WebSocket/Mutator объединять через `batch`.
  - Перед передачей данных API, Lexical и сторонним библиотекам использовать
    обычные объекты, а не store proxies.
  - Компоненты получают store через `AppStoreProvider/useAppStore`; Mutator и
    WebSocket получают actions через внедрение зависимостей.

- Заменить инфраструктуру React:
  - `react-router-dom` → `@solidjs/router`, сохранив все URL, optional parameters,
    redirects, login guards, query strings и динамический `window.baseURL`.
  - `react-intl` → `@formatjs/intl` с локальными Solid-компонентами
    `IntlProvider`, `useIntl` и `FormattedMessage`. Сохранить `IntlShape`, message
    IDs, ICU-формат и существующие JSON-переводы.
  - Hooks перенести на signals, memos, effects и lifecycle; не деструктурировать
    реактивные props. Отдельно проверить семантику controlled inputs,
    `onInput`/`onChange`, refs и cleanup.
  - React portals заменить на Solid `<Portal>`, context — на Solid context,
    `React.memo/useCallback` удалить.

- Заменить React-only интеграции:
  - Оставить Lexical core, реализовав Solid primitive для создания editor,
    регистрации plugins/listeners, history, mentions, emoji и cleanup; удалить
    `@lexical/react`.
  - FullCalendar использовать через его DOM API без React adapter.
  - DnD канбана, таблицы, blocks editor и sidebar унифицировать на
    `@dnd-kit/solid`, сохранив mouse, touch, keyboard, cross-container drop и
    autoscroll.
  - `react-select` заменить на `@thisbeyond/solid-select` с прежними CSS-классами
    и поведением.
  - Tippy и Emoji Mart использовать через framework-neutral API/web components.
  - Date picker и hotkeys оформить как внутренние Solid primitives, сохранив
    клавиатурную навигацию и accessibility.

## Реализация

1. Зафиксировать React-baseline: зелёные Jest/Cypress/packaging проверки,
   эталонные скриншоты и performance-профиль большой детерминированной доски.
   Сначала завершить и закоммитить уже начатый Cypress 14 upgrade.
2. Настроить Vite SPA с `vite-plugin-solid`, TypeScript JSX
   `preserve`/`jsxImportSource`, SCSS и Jest через `babel-jest` + Solid preset.
   Оставить отдельный Vite entry для dev editor.
3. Сохранить контракт сборки:
   - production output — `webapp/pack/index.html` и `pack/static/*`;
   - `index.html` содержит Go-шаблон `{{.BaseURL}}`;
   - entry CSS/JS имеют стабильные имена, остальные chunks и assets используют
     относительные ссылки;
   - `pack`, `watchdev`, `deveditor`, Makefile, Docker и Wails продолжают
     работать с прежними командами.
4. Перенести state layer, i18n, router, bootstrap, WebSocket и Mutator. После
   этого мигрировать UI от leaf widgets и property controls к
   sidebar/table/gallery/kanban/card detail, затем Lexical editor и app shell.
   SCSS и DOM-классы сохранять, редизайн не проводить.
5. Добавить lazy chunks для auth-страниц, calendar и editor. После полного
   cutover удалить React, ReactDOM, Redux, Webpack и все React adapters/types/plugins;
   обновить lockfile и licenses/NOTICE.

## Проверка и выпуск

- Перенести 144 Jest-сюиты на `@solidjs/testing-library`, сохранив поведенческое
  покрытие. Старые React snapshots сначала заменить семантическими
  DOM/assertion-проверками, затем осознанно создать новые Solid snapshots.
- Сохранить существующие Cypress-сценарии и добавить E2E для:
  - всех маршрутов, redirects и запуска под непустым BaseURL;
  - CRUD/undo/redo и WebSocket reconnect;
  - drag-and-drop между колонками, группами и sidebar на mouse/touch/keyboard;
  - editor mentions, emoji, paste и history;
  - select/date/calendar, локализации, mobile layout и dark/light themes.
- Проверять server и Wails packaging, шаблонизацию `index.html`, desktop
  bootstrap injection и прямое открытие вложенного URL.
- Добавить CI-guard, запрещающий импорты `react*`, `redux*`, `@lexical/react` и
  наличие соответствующих runtime dependencies.
- Performance-gates измерять пятью запусками в закреплённой версии Chromium на
  одной fixture:
  - initial gzip JS минимум на 20% меньше React-baseline;
  - медианное время от загрузки до интерактивной доски минимум на 15% лучше;
  - ни LCP, ни p95 длительности основных board interactions не хуже baseline
    более чем на 5%.
- Выпустить release candidate для web и desktop, выполнить smoke на
  Linux/macOS/Windows, затем единый production release. Откат — предыдущие
  server/desktop артефакты; миграций данных нет.

## Принятые допущения

- Используется клиентский SolidJS SPA, не SolidStart и не SSR.
- Поддерживаются современные браузеры уровня текущего SolidJS и актуальные
  Wails WebView; IE и устаревшие WebView не входят в контракт.
- Серверные endpoints, persisted board data, URL и пользовательское поведение
  остаются совместимыми.
- Новые продуктовые функции и визуальные изменения замораживаются до завершения
  миграционной ветки.
