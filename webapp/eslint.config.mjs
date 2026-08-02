// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Flat config, replacing .eslintrc.json + .eslintignore (ESLint 9 reads neither).
//
// The two files under .eslint/ are the configs `plugin:mattermost/react` used to
// pull in, vendored from github:mattermost/eslint-plugin-mattermost@23abcf99 -- see
// docs/npm-dependency-warnings.md. Flat config has no `extends`, so only their
// `rules` are read here; their `parser`/`plugins`/`env` keys are what this file
// expresses directly.

import js from '@eslint/js'
import stylistic from '@stylistic/eslint-plugin'
import tsPlugin from '@typescript-eslint/eslint-plugin'
import tsParser from '@typescript-eslint/parser'
import cypress from 'eslint-plugin-cypress'
import header from 'eslint-plugin-header'
import importPlugin from 'eslint-plugin-import'
import noOnlyTests from 'eslint-plugin-no-only-tests'
import react from 'eslint-plugin-react'
import reactHooks from 'eslint-plugin-react-hooks'
import globals from 'globals'

import mattermostBase from './.eslint/mattermost-base.json' with {type: 'json'}
import mattermostReact from './.eslint/mattermost-react.json' with {type: 'json'}

// eslint-plugin-header still ships an eslintrc-era `meta.schema`, which ESLint 9
// rejects when it validates rule options. The rule itself works; only the schema
// is stale, so drop it rather than lose the licence-header check.
header.rules.header.meta.schema = false

// The vendored base config carries an override for test files; keep it as the one
// place those relaxations are written down.
const mattermostBaseTestOverride = mattermostBase.overrides.
    find((o) => o.files.includes('tests/**'))

export default [
    {
        // Flat config replaces .eslintignore. `pack` and `coverage` are build output
        // that the eslintrc setup never reached because it only ran with --ext ts,tsx.
        ignores: ['node_modules/', 'wailsjs/', 'pack/', 'coverage/'],
    },
    {
        files: ['**/*.ts', '**/*.tsx'],

        languageOptions: {
            parser: tsParser,
            ecmaVersion: 2018,
            sourceType: 'module',
            parserOptions: {
                ecmaFeatures: {jsx: true, impliedStrict: true},
            },
            globals: {
                ...globals.browser,
                ...globals.node,
                ...globals.jest,
            },
        },

        plugins: {
            '@stylistic': stylistic,
            '@typescript-eslint': tsPlugin,
            'header': header,
            'import': importPlugin,
            'no-only-tests': noOnlyTests,
            react,
            'react-hooks': reactHooks,
        },

        settings: {
            'import/resolver': 'node',
            react: {
                pragma: 'React',
                version: 'detect',
            },
        },

        rules: {
            ...js.configs.recommended.rules,
            ...mattermostBase.rules,
            ...react.configs.recommended.rules,
            ...mattermostReact.rules,

            // What `extends: ["plugin:@typescript-eslint/recommended"]` used to mean:
            // first the layer that switches off core rules TypeScript already covers
            // (no-undef and friends), then the recommended set itself. Order matters --
            // this is where the eslintrc override applied them, i.e. after the base.
            ...tsPlugin.configs['eslint-recommended'].overrides[0].rules,
            ...tsPlugin.configs.recommended.rules,

            // Rules of React, which React Compiler needs held before it can be
            // switched on: it bails out of any component that breaks them.
            // rules-of-hooks is the one that is already clean and must stay so --
            // a hook behind an `if` is a latent bug whatever the compiler does.
            // The rest still report (they are what the compiler will complain
            // about) but as warnings, because 79 findings predate this rule set
            // and gating the build on them before anyone has read them would only
            // teach people to silence them.
            ...Object.fromEntries(Object.keys(reactHooks.configs['recommended-latest'].rules).
                map((rule) => [rule, rule === 'react-hooks/rules-of-hooks' ? 'error' : 'warn'])),

            // Formatting rules that typescript-eslint 8 handed over to @stylistic.
            // Same options as before, new owner.
            '@typescript-eslint/indent': 'off',
            '@typescript-eslint/member-delimiter-style': 'off',
            '@typescript-eslint/semi': 'off',
            '@typescript-eslint/type-annotation-spacing': 'off',
            '@stylistic/indent': [2, 4, {SwitchCase: 0}],
            '@stylistic/member-delimiter-style': [2, {
                multiline: {delimiter: 'none'},
                singleline: {delimiter: 'comma'},
            }],
            '@stylistic/semi': [2, 'never'],
            '@stylistic/type-annotation-spacing': 2,

            // ---- from the root .eslintrc.json, top level ----
            'max-lines': 'off',
            'no-unused-expressions': 0,

            // was babel/no-unused-expressions; eslint-plugin-babel is unmaintained and
            // typescript-eslint ships the same rule with the same options.
            '@typescript-eslint/no-unused-expressions': [2, {allowShortCircuit: true}],

            'eol-last': ['error', 'always'],
            'import/no-unresolved': 0, // ts handles this better than the resolver does
            'import/order': [
                2,
                {
                    'newlines-between': 'always-and-inside-groups',
                    groups: [
                        'builtin',
                        'external',
                        ['internal', 'parent'],
                        'sibling',
                        'index',
                    ],
                },
            ],
            'no-undefined': 0,
            'react/jsx-filename-extension': 0,
            'react/prop-types': [2, {ignore: ['location', 'history', 'component']}],
            'react/no-string-refs': 2,
            'no-only-tests/no-only-tests': ['error', {focus: ['only', 'skip']}],
            'max-nested-callbacks': ['error', {max: 5}],
            'no-shadow': 'off',
            '@typescript-eslint/no-shadow': 'error',

            // ---- from the root .eslintrc.json, the *.ts/*.tsx override ----
            camelcase: 0,
            semi: 'off',
            '@typescript-eslint/naming-convention': [
                2,
                {selector: 'function', format: ['camelCase', 'PascalCase']},
                {selector: 'variable', format: ['camelCase', 'PascalCase', 'UPPER_CASE']},
                {selector: 'parameter', format: ['camelCase', 'PascalCase'], leadingUnderscore: 'allow'},
                {selector: 'typeLike', format: ['PascalCase']},
            ],
            '@typescript-eslint/no-non-null-assertion': 0,

            // caughtErrors defaulted to 'none' up to typescript-eslint 7 and to 'all'
            // from 8. Spelling out the old value keeps this a tooling upgrade rather
            // than a new rule; `catch (e) {}` that ignores e is deliberate here.
            '@typescript-eslint/no-unused-vars': [2, {vars: 'all', args: 'after-used', caughtErrors: 'none'}],

            // typescript-eslint 6 promoted this from 'warn' to 'error' in recommended.
            // --quiet means warnings never failed the build, so error would be a new
            // gate on 151 existing call sites; keep the severity we actually had.
            '@typescript-eslint/no-explicit-any': 'warn',

            // typescript-eslint 6 moved these two out of `recommended` into
            // `stylistic`, which we don't extend. They were active and passing before,
            // so name them rather than lose them to a config reshuffle.
            '@typescript-eslint/adjacent-overload-signatures': 2,
            '@typescript-eslint/no-inferrable-types': 2,

            // Up to typescript-eslint 7, recommended switched the core rule off and
            // owned it; 8 dropped it from recommended entirely, which handed the core
            // rule back. We turn the TS one off just below, so turn off both.
            'no-empty-function': 'off',

            // was @typescript-eslint/no-var-requires, renamed in typescript-eslint 8
            '@typescript-eslint/no-require-imports': 0,

            '@typescript-eslint/no-empty-function': 0,
            '@typescript-eslint/explicit-function-return-type': 0,
            'no-use-before-define': 'off',
            '@typescript-eslint/no-use-before-define': [
                2,
                {classes: false, functions: false, variables: false},
            ],
            'no-useless-constructor': 0,
            '@typescript-eslint/no-useless-constructor': 2,
        },
    },
    {
        files: ['**/tests/**', '**/*.test.*'],
        languageOptions: {globals: globals.jest},
        rules: {
            ...mattermostBaseTestOverride.rules,

            // ---- from the root .eslintrc.json test override ----
            'func-names': 0,
            'global-require': 0,
            'new-cap': 0,
            'prefer-arrow-callback': 0,
            'no-import-assign': 0,
        },
    },
    {
        files: ['cypress/**'],
        ...cypress.configs.recommended,
        rules: {
            ...cypress.configs.recommended.rules,

            // New in eslint-plugin-cypress 6; it did not exist in the 2.x this repo
            // ran before. It has 30 findings, all `.type().type().should()` chains,
            // and rewriting them is a change to an E2E suite that is currently 5/11
            // red and runs in no workflow -- see the follow-up table at the end of
            // docs/build-toolchain-upgrade.md. Fix it there, with a green suite to
            // check against, not inside a lint upgrade.
            'cypress/unsafe-to-chain-command': 0,

            'cypress/no-unnecessary-waiting': 0,
            'func-names': 0,
            'import/no-unresolved': 0,
            'max-nested-callbacks': 0,
            'no-process-env': 0,
            'no-unused-expressions': 0,
        },
    },
]
