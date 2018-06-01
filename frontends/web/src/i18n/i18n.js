import i18n from 'i18next';
import appTranslationsDE from './de';
import appTranslationsEN from './en';
import { apiGet, apiPost } from '../utils/request';
import { userLanguage } from '../utils/config';
import languageDetector from './detect';

i18n
    .use(languageDetector)
    .init({
        // lng: userLanguage,
        fallbackLng: userLanguage || 'en',

        // have a common namespace used around the full app
        ns: ['app', 'wallet'],
        defaultNS: 'app',

        debug: false,
        returnObjects: true,

        interpolation: {
            escapeValue: false // not needed for react
        },

        react: {
            wait: true
        }
    });


i18n.addResourceBundle('en', 'app', appTranslationsEN);
i18n.addResourceBundle('de', 'app', appTranslationsDE);

i18n.on('languageChanged', (lng) => {
    apiGet('config').then((config) => {
        const newConf = Object.assign(config, {
            frontend: Object.assign({}, config.frontend, {
                userLanguage: lng
            })
        });
        apiPost('config', newConf);
    });
});

export default i18n;
