'use client';

import { useState, type FormEvent } from 'react';
import {
  FORM_NAME,
  HONEYPOT_NAME,
  REMOVAL_ADDRESS,
  ROLES,
  interestMode,
  interestProvider,
} from './interest';

type Status = 'idle' | 'sending' | 'sent' | 'error';
type Errors = { email?: string; consent?: string };

const EMAIL_ERROR = 'Enter a valid email address.';
const CONSENT_ERROR = 'Please agree so we can email you.';
const SENT_MESSAGE = 'Thanks. We have your details.';
const FAILED_MESSAGE = 'That did not go through. Check your email address and try again.';

function field(form: HTMLFormElement, name: string): HTMLInputElement {
  return form.elements.namedItem(name) as HTMLInputElement;
}

function validate(form: HTMLFormElement): Errors {
  const errors: Errors = {};
  const email = field(form, 'email');
  if (!email.value.trim() || !email.validity.valid) errors.email = EMAIL_ERROR;
  if (!field(form, 'consent').checked) errors.consent = CONSENT_ERROR;
  return errors;
}

function readValues(form: HTMLFormElement) {
  return {
    email: field(form, 'email').value.trim(),
    company: field(form, 'company').value.trim(),
    role: (form.elements.namedItem('role') as HTMLSelectElement).value,
    consent: field(form, 'consent').checked,
    botField: field(form, HONEYPOT_NAME).value,
  };
}

function StaticNetlifyForm() {
  return (
    <form name={FORM_NAME} method="POST" data-netlify="true" netlify-honeypot={HONEYPOT_NAME} hidden>
      <input type="hidden" name="form-name" value={FORM_NAME} />
      <input name={HONEYPOT_NAME} />
      <input type="email" name="email" />
      <input name="company" />
      <input name="role" />
      <input type="checkbox" name="consent" />
    </form>
  );
}

function PrivacyNote() {
  return (
    <p className="pools-note">
      We store your email address and, if you give them, your company and role. Netlify, our form host,
      stores these submissions for us, with the date and time you sent the form. We use them only to contact
      you about Devsy Pools. We do not sell them or share them with other companies. Netlify passes each
      submission to Akismet, a spam filter, to check it for spam. To have your data
      deleted, email <a className="pools-inline-link" href={`mailto:${REMOVAL_ADDRESS}`}>{REMOVAL_ADDRESS}</a>.
    </p>
  );
}

function FieldError({ id, message }: { id: string; message?: string }) {
  return (
    <p id={id} className="pools-error" hidden={!message}>
      {message}
    </p>
  );
}

function Fields({ errors }: { errors: Errors }) {
  return (
    <>
      <div className="field">
        <label htmlFor="pools-email">Work email</label>
        <input
          id="pools-email"
          type="email"
          name="email"
          autoComplete="email"
          required
          aria-invalid={errors.email ? true : undefined}
          aria-describedby="pools-email-error"
        />
        <FieldError id="pools-email-error" message={errors.email} />
      </div>
      <div className="form-row">
        <div className="field">
          <label htmlFor="pools-company">
            Company <span className="optional">(optional)</span>
          </label>
          <input id="pools-company" type="text" name="company" autoComplete="organization" />
        </div>
        <div className="field">
          <label htmlFor="pools-role">
            Role <span className="optional">(optional)</span>
          </label>
          <select id="pools-role" name="role" defaultValue="">
            <option value="">Choose a role</option>
            {ROLES.map((role) => (
              <option key={role} value={role}>
                {role}
              </option>
            ))}
          </select>
        </div>
      </div>
    </>
  );
}

function Consent({ error }: { error?: string }) {
  return (
    <div className="field pools-consent">
      <label htmlFor="pools-consent">
        <input
          id="pools-consent"
          type="checkbox"
          name="consent"
          aria-invalid={error ? true : undefined}
          aria-describedby="pools-consent-error"
        />
        <span>I agree that Devsy may email me about Devsy Pools.</span>
      </label>
      <FieldError id="pools-consent-error" message={error} />
    </div>
  );
}

function Form() {
  const [status, setStatus] = useState<Status>('idle');
  const [errors, setErrors] = useState<Errors>({});

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    if (status === 'sending') return;
    const found = validate(form);
    setErrors(found);
    if (found.email || found.consent) {
      document.getElementById(found.email ? 'pools-email' : 'pools-consent')?.focus();
      return;
    }
    setStatus('sending');
    try {
      await interestProvider.submit(readValues(form));
      setStatus('sent');
      form.reset();
    } catch {
      setStatus('error');
    }
  }

  return (
    <form className="pools-form" onSubmit={onSubmit} noValidate>
      <Fields errors={errors} />
      <p className="form-hidden" aria-hidden="true">
        <label>
          Leave this field empty
          <input name={HONEYPOT_NAME} tabIndex={-1} autoComplete="off" />
        </label>
      </p>
      <Consent error={errors.consent} />
      <div className="form-actions">
        <button type="submit" className="btn btn-primary" disabled={status === 'sending'}>
          Register interest
        </button>
      </div>
      <div role="status" aria-live="polite" className="pools-status" data-state={status}>
        {status === 'sent' && SENT_MESSAGE}
        {status === 'error' && FAILED_MESSAGE}
      </div>
      <PrivacyNote />
    </form>
  );
}

export function InterestForm() {
  if (interestMode() === 'off') return null;
  return (
    <section id="pools-interest" className="pools-interest" aria-labelledby="pools-interest-title">
      <div className="wrap">
        <div className="section-head">
          <h2 id="pools-interest-title">Register your interest in Devsy Pools</h2>
          <p>Tell us you are interested. We use your details only to contact you about Devsy Pools.</p>
        </div>
        <Form />
        <StaticNetlifyForm />
      </div>
    </section>
  );
}
