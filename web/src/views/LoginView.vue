<script setup lang="ts">
// LoginView is a shell: it proves the session and CSRF contract end to end,
// not a working sign-in. On success the backend starts a session cookie
// (see internal/httpapi's handleLogin) and this view hands off to HomeView,
// whose own mount check is what actually exercises the session GET
// /api/v1/me needs. Real local accounts and provider sign-in replace this
// view's backing handler without changing this contract.
import {
  GovueButton,
  GovueCard,
  GovueField,
  GovueInput,
  GovuePage,
  useForm,
} from '@ostendorf-cloud/govue'
import type { FieldErrors } from '@ostendorf-cloud/govue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { apiClient } from '../api'

const { t } = useI18n()
const router = useRouter()

interface LoginFields {
  email: string
}

const form = useForm<LoginFields>({
  initial: { email: '' },
  validate: (values) => {
    const errors: FieldErrors<LoginFields> = {}
    if (values.email.trim() === '') {
      errors.email = t('login.validation.email')
    }
    return errors
  },
  submit: async (values) => {
    await apiClient.post('/auth/login', { email: values.email })
    await router.push({ name: 'home' })
  },
})
</script>

<template>
  <GovuePage :title="t('login.title')" :lead="t('login.lead')">
    <GovueCard>
      <!-- novalidate: the required attribute below stays for accessibility
           and the field's own styling, but validation feedback is
           useForm's own i18n'd messages, never the browser's native,
           unlocalized constraint validation tooltip. -->
      <form class="flex flex-col gap-4" novalidate @submit.prevent="form.handleSubmit">
        <GovueField :label="t('login.email')" :error="form.errorOf('email')" required>
          <GovueInput
            v-model="form.values.email"
            type="email"
            autocomplete="username"
            @blur="form.blur('email')"
          />
        </GovueField>

        <p v-if="form.problem.value" role="alert" class="text-sm text-danger-text">
          {{ form.problem.value }}
        </p>

        <GovueButton
          type="submit"
          variant="primary"
          block
          :loading="form.submitting.value"
          :loading-label="t('login.submitting')"
        >
          {{ t('login.submit') }}
        </GovueButton>
      </form>
    </GovueCard>
  </GovuePage>
</template>
