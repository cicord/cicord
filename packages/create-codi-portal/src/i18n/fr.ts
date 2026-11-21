/**
 * Interface copy (French). Fork the portal and translate this file to change the language;
 * texts that come from the server config (step titles, success message…) are not here.
 */
export const t = {
  locale: "fr-FR",
  loading: "Chargement…",
  portalOf: (server: string) => `Le portail de ${server}`,
  login: "Se connecter avec Discord",
  loginShort: "Se connecter",
  logout: "Se déconnecter",
  loggingIn: "Connexion en cours…",
  profile: "Mon profil",
  demoMode: "Démo : données fictives, aucune connexion à Discord",

  home: {
    loggedOutTitle: "Connecte-toi pour commencer",
    loggedOut: "Avec ton compte Discord, choisis tes rôles et retrouve ton profil de membre.",
    hello: (name: string) => `Salut ${name} !`,
    onboardingTitle: "Choisir mes rôles",
    onboardingTodo: "Quelques questions pour débloquer tes salons.",
    onboardingDone: "C'est fait. Tu peux changer tes réponses quand tu veux.",
    profileTitle: "Mon profil",
    profileText: "Ton pseudo, tes rôles et ta date d'arrivée.",
    notMember:
      "Ton compte Discord n'est pas encore membre de ce serveur. Rejoins-le, puis reviens ici.",
  },

  onboarding: {
    step: (n: number, total: number) => `Question ${n} sur ${total}`,
    progress: "Progression",
    multiple: "Plusieurs choix possibles",
    single: "Un seul choix",
    optional: "Facultatif",
    required: "Obligatoire",
    back: "Retour",
    next: "Suivant",
    finish: "Valider mes rôles",
    saving: "Enregistrement…",
    pickOne: "Choisis au moins une option pour continuer.",
    previewTitle: "Aperçu de ton profil",
    previewRoles: "Rôles",
    noRoles: "Aucun rôle pour l'instant.",
    doneTitle: "Tes rôles sont à jour",
    added: (n: number) =>
      n === 0 ? "Aucun nouveau rôle." : n === 1 ? "1 nouveau rôle." : `${n} nouveaux rôles.`,
    openDiscord: "Retourner sur Discord",
    edit: "Modifier mes réponses",
    loginTitle: "Connecte-toi pour choisir tes rôles",
    loginText:
      "Le lien envoyé par le bot te connecte automatiquement. Tu peux aussi te connecter avec ton compte Discord.",
    linkFailed:
      "Ce lien a expiré ou a déjà servi. Tape /onboarding sur Discord pour en recevoir un nouveau, ou connecte-toi.",
    unavailable: "L'onboarding n'est pas activé sur ce serveur.",
    forbidden: "Tu dois être membre du serveur Discord pour choisir tes rôles.",
    failed: "Impossible de charger les questions.",
    retry: "Réessayer",
  },

  me: {
    title: "Mon profil",
    since: (date: string) => `Membre depuis le ${date}`,
    roles: "Rôles",
    access: { admin: "Administrateur", moderator: "Modérateur", member: "Membre" } as Record<
      string,
      string
    >,
    editRoles: "Modifier mes rôles",
    loginTitle: "Connecte-toi pour voir ton profil",
  },

  notFound: {
    title: "Page introuvable",
    text: "Ce lien ne mène nulle part. Retourne à l'accueil du portail.",
    home: "Accueil",
  },

  noServer: {
    title: "Aucun portail ici",
    text: "Ce runtime CODI ne publie de portail pour aucun serveur. Ajoute `portal:` dans servers/<serveur>.yaml.",
  },
  unknownServer: (key: string) => `Le serveur « ${key} » n'a pas de portail.`,
  apiDown: "Le portail ne répond pas. Réessaie dans un instant.",
};
