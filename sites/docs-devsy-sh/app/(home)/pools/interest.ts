export type InterestMode = 'off' | 'on';

export interface InterestValues {
  email: string;
  company: string;
  role: string;
  consent: boolean;
  botField: string;
}

export interface InterestProvider {
  submit(values: InterestValues): Promise<void>;
}

export const FORM_NAME = 'pools-interest';
export const HONEYPOT_NAME = 'bot-field';
export const REMOVAL_ADDRESS = 'hello@devsy.sh';

export const ROLES = [
  'Platform or developer experience',
  'Security',
  'Engineering leadership',
  'Developer',
  'Other',
] as const;

// Setting NEXT_PUBLIC_POOLS_INTEREST_FORM=off at build time closes the form.
export function interestMode(): InterestMode {
  return process.env.NEXT_PUBLIC_POOLS_INTEREST_FORM === 'off' ? 'off' : 'on';
}

// Netlify Forms reads a static copy of the form from the built HTML, so the
// visible form posts the same field names to the site root.
const netlifyProvider: InterestProvider = {
  async submit(values) {
    const body = new URLSearchParams({
      'form-name': FORM_NAME,
      email: values.email,
      company: values.company,
      role: values.role,
      [HONEYPOT_NAME]: values.botField,
    });
    if (values.consent) body.set('consent', 'yes');
    const response = await fetch('/', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString(),
    });
    if (!response.ok) throw new Error(`form host returned ${response.status}`);
  },
};

export const interestProvider: InterestProvider = netlifyProvider;
