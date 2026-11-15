import prettier from '@vue/eslint-config-prettier'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'
import pluginVue from 'eslint-plugin-vue'

const config: ReturnType<typeof defineConfigWithVueTs> = defineConfigWithVueTs(
  {
    name: 'calbridge-landing/files',
    files: ['**/*.ts', '**/*.vue'],
  },
  {
    name: 'calbridge-landing/ignores',
    ignores: ['dist/**', 'node_modules/**'],
  },
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommendedTypeChecked,
  prettier,
  {
    name: 'calbridge-landing/rules',
    rules: {
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-non-null-assertion': 'error',
      '@typescript-eslint/consistent-type-imports': 'error',
      // The public site is a static build: it makes no API call, holds
      // no session and knows no CSRF token, which is what lets its text
      // be corrected without redeploying the application. Nothing in
      // the toolchain enforces that on this side of the split, so these
      // two restrictions do.
      //
      // createClient is govue's API client, and the only thing this
      // package could build one for is a call to the application. The
      // patterns cover the other way in: a relative import reaching out
      // of web-landing/ and into web/, which would pull the
      // application's own api module, its views and its session
      // handling across the package boundary.
      '@typescript-eslint/no-restricted-imports': [
        'error',
        {
          paths: [
            {
              name: '@ostendorf-cloud/govue',
              importNames: ['createClient'],
              message:
                'The public site makes no API call: it carries no session and no CSRF token. ' +
                'Link to the application through appUrl instead.',
            },
          ],
          patterns: [
            {
              group: ['**/web/src/**', '../web/**', '../../web/**'],
              message:
                'web-landing/ is a separate package from web/. Copy what is needed, the way ' +
                'src/theme/ is its own copy, rather than importing across the two.',
            },
          ],
        },
      ],
    },
  },
  {
    name: 'calbridge-landing/tests',
    files: ['**/*.test.ts'],
    rules: {
      '@typescript-eslint/unbound-method': 'off',
      'vue/one-component-per-file': 'off',
    },
  },
)

export default config
