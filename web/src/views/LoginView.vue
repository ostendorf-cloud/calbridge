<script setup lang="ts">
// LoginView is calbridge's real sign-in: local accounts with password and
// TOTP, and sign-in through whatever providers internal/auth has configured
// (see internal/httpapi's provider list and login/signup handlers). Signing
// up never starts a session; it only hands back the TOTP secret and
// otpauth URI once, which is what the success view below shows so the
// person can add the account to an authenticator app before they sign in.
// Provider sign-in is a browser redirect, not a fetch: the backend answers
// with a redirect to the provider's own consent screen, so this view only
// navigates to it.
import { onMounted, ref } from 'vue'
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

type Mode = 'sign-in' | 'sign-up'
const mode = ref<Mode>('sign-in')

interface SignInFields {
  email: string
  password: string
  totpCode: string
}

interface SignUpFields {
  email: string
  password: string
}

interface SignupResponse {
  totp_secret: string
  totp_uri: string
}

interface SignupResult {
  totpSecret: string
  totpUri: string
}

interface ProviderResponse {
  id: string
  name: string
  identity_only: boolean
  placeholder_email: boolean
}

interface ProvidersResponse {
  providers: ProviderResponse[]
}

interface Provider {
  id: string
  name: string
  identityOnly: boolean
  // True for a provider that hands over no address this application
  // will believe, so the account ends up on a placeholder one that
  // cannot be changed yet. Said before the person picks the provider,
  // not after.
  placeholderEmail: boolean
}

const providers = ref<Provider[]>([])
const providersError = ref('')

onMounted(async () => {
  try {
    const response = await apiClient.get<ProvidersResponse>('/auth/providers')
    providers.value = response.providers.map((provider) => ({
      id: provider.id,
      name: provider.name,
      identityOnly: provider.identity_only,
      placeholderEmail: provider.placeholder_email,
    }))
  } catch {
    providersError.value = t('login.providers.loadError')
  }
})

// Never a fetch: the backend's handler for this path answers with a
// redirect to the provider's own consent screen, which only a real
// navigation follows.
function signInWithProvider(id: string): void {
  window.location.assign(`/api/v1/auth/providers/${id}/login`)
}

const signupResult = ref<SignupResult | undefined>(undefined)

function switchToSignUp(): void {
  mode.value = 'sign-up'
}

function switchToSignIn(): void {
  mode.value = 'sign-in'
  signupResult.value = undefined
  signUpForm.reset()
}

const signInForm = useForm<SignInFields>({
  initial: { email: '', password: '', totpCode: '' },
  validate: (values) => {
    const errors: FieldErrors<SignInFields> = {}
    if (values.email.trim() === '') {
      errors.email = t('login.signIn.validation.email')
    }
    if (values.password === '') {
      errors.password = t('login.signIn.validation.password')
    }
    if (values.totpCode.trim() === '') {
      errors.totpCode = t('login.signIn.validation.totpCode')
    }
    return errors
  },
  submit: async (values) => {
    await apiClient.post('/auth/login', {
      email: values.email,
      password: values.password,
      totp_code: values.totpCode,
    })
    await router.push({ name: 'home' })
  },
})

const signUpForm = useForm<SignUpFields>({
  initial: { email: '', password: '' },
  validate: (values) => {
    const errors: FieldErrors<SignUpFields> = {}
    if (values.email.trim() === '') {
      errors.email = t('login.signUp.validation.email')
    }
    if (values.password === '') {
      errors.password = t('login.signUp.validation.password')
    }
    return errors
  },
  submit: async (values) => {
    const response = await apiClient.post<SignupResponse>('/auth/signup', {
      email: values.email,
      password: values.password,
    })
    signupResult.value = { totpSecret: response.totp_secret, totpUri: response.totp_uri }
  },
})
</script>

<template>
  <GovuePage :title="t('login.title')" :lead="t('login.lead')">
    <GovueCard v-if="mode === 'sign-in'">
      <!-- novalidate: the required attribute below stays for accessibility
           and the field's own styling, but validation feedback is
           useForm's own i18n'd messages, never the browser's native,
           unlocalized constraint validation tooltip. -->
      <form class="flex flex-col gap-4" novalidate @submit.prevent="signInForm.handleSubmit">
        <GovueField :label="t('login.signIn.email')" :error="signInForm.errorOf('email')" required>
          <GovueInput
            v-model="signInForm.values.email"
            type="email"
            autocomplete="username"
            @blur="signInForm.blur('email')"
          />
        </GovueField>

        <GovueField
          :label="t('login.signIn.password')"
          :error="signInForm.errorOf('password')"
          required
        >
          <GovueInput
            v-model="signInForm.values.password"
            type="password"
            autocomplete="current-password"
            @blur="signInForm.blur('password')"
          />
        </GovueField>

        <GovueField
          :label="t('login.signIn.totpCode')"
          :error="signInForm.errorOf('totpCode')"
          required
        >
          <GovueInput
            v-model="signInForm.values.totpCode"
            type="text"
            inputmode="numeric"
            autocomplete="one-time-code"
            @blur="signInForm.blur('totpCode')"
          />
        </GovueField>

        <p v-if="signInForm.problem.value" role="alert" class="text-sm text-danger-text">
          {{ signInForm.problem.value }}
        </p>

        <GovueButton
          type="submit"
          variant="primary"
          block
          :loading="signInForm.submitting.value"
          :loading-label="t('login.signIn.submitting')"
        >
          {{ t('login.signIn.submit') }}
        </GovueButton>

        <GovueButton type="button" variant="quiet" block @click="switchToSignUp">
          {{ t('login.signIn.switchToSignup') }}
        </GovueButton>
      </form>
    </GovueCard>

    <GovueCard v-else :title="t('login.signUp.title')">
      <form
        v-if="!signupResult"
        class="flex flex-col gap-4"
        novalidate
        @submit.prevent="signUpForm.handleSubmit"
      >
        <GovueField :label="t('login.signUp.email')" :error="signUpForm.errorOf('email')" required>
          <GovueInput
            v-model="signUpForm.values.email"
            type="email"
            autocomplete="username"
            @blur="signUpForm.blur('email')"
          />
        </GovueField>

        <GovueField
          :label="t('login.signUp.password')"
          :error="signUpForm.errorOf('password')"
          required
        >
          <GovueInput
            v-model="signUpForm.values.password"
            type="password"
            autocomplete="new-password"
            @blur="signUpForm.blur('password')"
          />
        </GovueField>

        <p v-if="signUpForm.problem.value" role="alert" class="text-sm text-danger-text">
          {{ signUpForm.problem.value }}
        </p>

        <GovueButton
          type="submit"
          variant="primary"
          block
          :loading="signUpForm.submitting.value"
          :loading-label="t('login.signUp.submitting')"
        >
          {{ t('login.signUp.submit') }}
        </GovueButton>

        <GovueButton type="button" variant="quiet" block @click="switchToSignIn">
          {{ t('login.signUp.switchToSignin') }}
        </GovueButton>
      </form>

      <!-- The secret and the otpauth URI are shown exactly once: the
           backend never answers with them again, so this is the only
           chance to add the account to an authenticator app. No QR code
           is rendered here; that needs a library this view does not add. -->
      <div v-else class="flex flex-col gap-4">
        <p>{{ t('login.signUp.successLead') }}</p>

        <div>
          <p class="text-sm font-medium text-text-muted">{{ t('login.signUp.secretLabel') }}</p>
          <p class="font-mono text-sm break-all">{{ signupResult.totpSecret }}</p>
        </div>

        <div>
          <p class="text-sm font-medium text-text-muted">{{ t('login.signUp.uriLabel') }}</p>
          <p class="font-mono text-sm break-all">{{ signupResult.totpUri }}</p>
        </div>

        <GovueButton type="button" variant="primary" block @click="switchToSignIn">
          {{ t('login.signUp.continue') }}
        </GovueButton>
      </div>
    </GovueCard>

    <GovueCard v-if="providers.length > 0 || providersError" :title="t('login.providers.title')">
      <p v-if="providersError" role="alert" class="text-sm text-danger-text">
        {{ providersError }}
      </p>

      <div v-else class="flex flex-col gap-3">
        <div v-for="provider in providers" :key="provider.id" class="flex flex-col gap-1">
          <GovueButton
            type="button"
            variant="secondary"
            block
            @click="signInWithProvider(provider.id)"
          >
            {{ t('login.providers.signInWith', { name: provider.name }) }}
          </GovueButton>
          <p v-if="provider.identityOnly" class="text-sm text-text-muted">
            {{ t('login.providers.identityOnly') }}
          </p>
          <p v-if="provider.placeholderEmail" class="text-sm text-text-muted">
            {{ t('login.providers.placeholderEmail', { name: provider.name }) }}
          </p>
        </div>
      </div>
    </GovueCard>
  </GovuePage>
</template>
