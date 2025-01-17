import { module, test } from 'qunit';
import { setupApplicationTest } from 'ember-qunit';
import { click, visit, fillIn } from '@ember/test-helpers';
import { setupMirage } from 'ember-cli-mirage/test-support';
import { fakeWindow, buildMessage } from '../helpers/oidc-window-stub';
import sinon from 'sinon';
import { later, _cancelTimers as cancelTimers } from '@ember/runloop';

module('Acceptance | oidc auth method', function (hooks) {
  setupApplicationTest(hooks);
  setupMirage(hooks);

  hooks.beforeEach(function () {
    this.openStub = sinon.stub(window, 'open').callsFake(() => fakeWindow.create());
    this.server.post('/auth/oidc/oidc/auth_url', () => ({
      data: { auth_url: 'http://example.com' },
    }));
    this.server.get('/auth/foo/oidc/callback', () => ({
      auth: { client_token: 'root' },
    }));
    // ensure clean state
    sessionStorage.removeItem('selectedAuth');
  });
  hooks.afterEach(function () {
    this.openStub.restore();
  });

  const login = async (select) => {
    await visit('/vault/auth');
    // select from dropdown or click auth path tab
    if (select) {
      await fillIn('[data-test-select="auth-method"]', 'oidc');
    } else {
      await click('[data-test-auth-method-link="oidc"]');
    }
    later(() => {
      window.postMessage(buildMessage().data, window.origin);
      cancelTimers();
    }, 50);
    await click('[data-test-auth-submit]');
  };

  test('it should login with oidc when selected from auth methods dropdown', async function (assert) {
    assert.expect(1);

    this.server.get('/auth/token/lookup-self', (schema, req) => {
      assert.ok(true, 'request made to auth/token/lookup-self after oidc callback');
      req.passthrough();
    });

    await login(true);
  });

  test('it should login with oidc from listed auth mount tab', async function (assert) {
    assert.expect(2);

    this.server.get('/sys/internal/ui/mounts', () => ({
      data: {
        auth: {
          'test-path/': { description: '', options: {}, type: 'oidc' },
        },
      },
    }));
    let didAssert;
    this.server.post('/auth/test-path/oidc/auth_url', () => {
      // request may be fired more than once -- we are only concerned if the endpoint is hit, not how many times
      if (!didAssert) {
        assert.ok(true, 'auth_url request made to correct non-standard mount path');
        didAssert = true;
      }
      return { data: { auth_url: 'http://example.com' } };
    });
    // there was a bug that would result in the /auth/:path/login endpoint hit with an empty payload rather than lookup-self
    // ensure that the correct endpoint is hit after the oidc callback
    this.server.get('/auth/token/lookup-self', (schema, req) => {
      assert.ok(true, 'request made to auth/token/lookup-self after oidc callback');
      req.passthrough();
    });
    await login();
  });

  // coverage for bug where token was selected as auth method for oidc and jwt
  test('it should populate oidc auth method on logout', async function (assert) {
    await login(true);
    await click('.nav-user-button button');
    await click('#logout');
    assert
      .dom('[data-test-select="auth-method"]')
      .hasValue('oidc', 'Previous auth method selected on logout');
  });
});
