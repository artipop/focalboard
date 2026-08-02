# React 19, React Compiler и отказ от ручной мемоизации

## Оценка

- React Compiler 1.x уже стабилен, рассчитан прежде всего на React 19 и автоматически заменяет большинство применений `React.memo`, `useMemo` и `useCallback`. Это соответствует [официальной рекомендации React](https://react.dev/learn/react-compiler/introduction).
- В webapp сейчас 90 `React.memo`, 37 `useMemo` и 157 `useCallback`. Hooks ESLint не подключён, а уже найден как минимум один условный Hook в router и несколько callback с заведомо устаревающими замыканиями. Поэтому compiler нельзя включать до исправления Rules of React.
- Выбран максимальный вариант: один PR, React Compiler в `infer`-режиме для всего приложения и удаление всей необязательной ручной мемоизации.
- Оценка всего PR: 21–34 инженерных дня, из них compiler, hooks-аудит, очистка мемоизации и профилирование — 5–8 дней. Риск высокий из-за одновременной замены React, Redux, DnD, календаря и модели рендеринга.

## React 19 и совместимые зависимости

- Довести React/React DOM до точных версий 19.2.x через промежуточный React 18.3 warning-аудит, как рекомендует [React 19 Upgrade Guide](https://react.dev/blog/2024/04/25/react-19-upgrade-guide).
- Обновить TypeScript до 5.9.x, React types до 19.x и включить `jsx: react-jsx`.
- Перевести оба entrypoint на `createRoot`; подключить `StrictMode`, чтобы в development выявлять нечистый render, некорректные Effects и ref cleanup.
- Исправить React 19 type changes: `React.JSX`, callback refs, обязательный аргумент `useRef`, явный `children`, неизвестные `ReactElement.props` и импорты `act` из `react`.
- Обновить:
  - React Redux 9.3.x, Redux Toolkit 2.12.x и Redux 5;
  - React Intl 7.1.11;
  - FullCalendar packages до единой версии 6.1.x;
  - React Testing Library 16.3.x, DOM Testing Library 10.x, user-event 14.x и jest-dom 6.x;
  - Jest, ts-jest и FormatJS transformer до peer-совместимого комплекта.
- Удалить устаревшие или неиспользуемые React-зависимости, включая `mini-create-react-context`, `react-hot-keys`, старый FullCalendar meta-package и отдельные `@types` библиотек со встроенными типами.
- Установка должна проходить без `--force`, `--legacy-peer-deps`, overrides и невалидных peer-зависимостей.

## React Compiler и современная модель оптимизации

- Подключить точные стабильные версии `babel-plugin-react-compiler` 1.x и `react-compiler-webpack`. Официальная документация React для webpack ссылается на этот [webpack loader](https://github.com/SukkaW/react-compiler-webpack).
- В webpack цепочке запускать compiler по исходному TS/TSX до `ts-loader`; существующий FormatJS TypeScript transformer должен выполняться после compiler.
- Использовать одинаковую compiler-конфигурацию для dev, production и editor builds:
  - `target: '19'`;
  - `compilationMode: 'infer'`;
  - `panicThreshold: 'none'`;
  - исключить `node_modules`, generated и test-файлы.
- Подключить `eslint-plugin-react-hooks` с `recommended-latest`. Он проверяет Rules of Hooks, purity, immutability, refs, effect dependencies и совместимость с compiler — полный список описан в [официальной документации](https://react.dev/reference/eslint-plugin-react-hooks).
- Исправить все diagnostics в app-коде, а не подавлять их:
  - убрать условные Hooks;
  - вынести side effects и глобальные мутации из render;
  - исправить мутацию props/state/selector results;
  - устранить stale closures и синхронный `setState` в Effects;
  - не создавать компоненты и Hooks внутри render.
- В финальном состоянии не оставлять `"use no memo"` и ESLint-disable для compiler rules. Сторонний код не компилировать.
- Добавить `npm run compiler:check`, который запускает compiler healthcheck и падает при новых несовместимых app-компонентах.
- Проверять реальное включение compiler по `_c`/`react/compiler-runtime` в неминифицированной dev-сборке и по Memo ✨ в React DevTools.

## Удаление memo/useMemo/useCallback

- Удалить все 90 обёрток `React.memo`: compiler автоматически применяет более гранулярный эквивалент memo и к компонентам, и к создаваемым JSX-узлам. Это поведение описано в [React memo reference](https://react.dev/reference/react/memo).
- Удалить `useCallback`, использовавшиеся только для передачи стабильного callback дочернему компоненту.
- Удалить `useMemo` для derived data, JSX, context values, конфигурационных объектов и локальных вычислений; compiler должен кэшировать их автоматически.
- Не заменять Hooks механически. Для identity, являющейся частью корректности, использовать подходящую модель:
  - `useRef` для mutable containers, singleton-подобных объектов и imperative handles;
  - функция внутри `useEffect` для add/remove event listeners;
  - `useEffectEvent` для Effect-обработчиков, которым нужны актуальные props/state без переподписки;
  - `useEffect` с обязательным cleanup для debounce/throttle, timers и subscriptions;
  - selector/Reselect для кэширования производных Redux-данных между потребителями;
  - module-level constants/functions для действительно статических значений.
- Исправить найденные проблемные места:
  - создавать browser history через безусловный Hook;
  - убрать callbacks с пустым dependency array, захватывающие dialog props;
  - перевести debounced search/mentions/sidebar updates на ref + cleanup;
  - заменить `useMemo(() => new Map(), [])` на `useRef`;
  - переписать resize и document listeners так, чтобы регистрация и удаление использовали одну semantic identity.
- Допускать оставшийся `useMemo`/`useCallback` только на внешней imperative-границе, где identity является частью контракта сторонней библиотеки. Каждое такое место снабдить коротким комментарием с причиной; performance-only исключений не оставлять.

## DnD и даты

- Полностью заменить `react-beautiful-dnd`, React DnD, backends и `react-dnd-scrolling` единым стабильным стеком `@dnd-kit/core`, `sortable`, `modifiers` и `utilities`.
- Использовать Pointer, Touch и Keyboard sensors, DragOverlay, accessibility announcements и autoscroll:
  - pointer distance 6 px;
  - touch delay 150 ms/tolerance 5 px;
  - sortable keyboard coordinates.
- Реализовать вложенный sortable sidebar, multi-container kanban и вертикальный block editor, сохранив multi-select, hidden groups, readonly, undo и persisted order.
- Заменить DayPicker 7 на `@daypicker/react` 10.x согласно [официальной миграции](https://daypicker.dev/upgrading).
- Создать общий controlled calendar для single/range выбора, добавить date-fns locale resolver, Today/Clear, доступную клавиатурную навигацию и новые CSS selectors.
- Не менять сохранённую схему `{from, to, includeTime, timeZone}` и существующую noon-UTC семантику date-only значений.

## Проверки и критерии приёмки

- `npm ci` и `npm ls` проходят без peer warnings, `invalid` и `extraneous`.
- `npm run check` включает hooks/compiler rules и не содержит suppressions.
- Пройти полный `npm run test`, `npm run pack` и `make mac-dmg-wails`.
- Добавить component-тесты для React root, StrictMode cleanup, исправленных Effects, debounce, event listeners и отсутствия stale callbacks.
- Добавить DnD-тесты для pointer, touch и keyboard во всех трёх зонах, включая multi-select, hidden groups и отсутствие drag при клике.
- Добавить date-тесты для single/range, Today/Clear, локалей, невалидного ввода и отсутствия timezone-сдвига.
- До удаления мемоизации снять React Profiler baseline на больших table/kanban-досках. После миграции сравнить минимум пять прогонов:
  - открытие и переключение представлений;
  - ввод в search;
  - массовое выделение и DnD;
  - открытие card detail;
  - resize колонок.
- Не принимать PR при ухудшении медианного commit duration более чем на 10%, росте числа commits/renders или пользовательски заметной задержке. Если compiler не улучшает конкретный hot path, сначала менять границы state/props и selectors, а не возвращать глобальный `memo`.
- Ручной smoke-test: Chrome, Firefox, Safari и touch-устройство; React DevTools должен показывать Memo ✨ на ключевых компонентах table, kanban, sidebar и card detail.

## Допущения

- React Compiler включается сразу для всего app-кода, без feature gating и annotation rollout.
- React Compiler оптимизирует только собственный webapp; зависимости из `node_modules` остаются как опубликованы.
- React Compiler не заменяет кэш данных, Redux selectors, debounce lifecycle или imperative identity — он заменяет render-time performance memoization.
- React Compiler не применяется к Jest-тестам; его фактическое поведение покрывают webpack build и ручное профилирование.
- React Router 6/7, React Compiler-driven архитектурная переработка state и новые React Actions/Suspense возможности остаются вне этого PR.
