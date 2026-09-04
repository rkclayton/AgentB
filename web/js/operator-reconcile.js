export function createOperatorReconciler(options) {
  let observedRevision = 0;
  let requestRevision = 0;

  return {
    observed() {
      observedRevision += 1;
    },

    async reconcile() {
      const observedAtStart = observedRevision;
      const request = ++requestRevision;
      const snapshot = await options.readState();
      if (request !== requestRevision || observedAtStart !== observedRevision) return false;
      if (!snapshot?.shell_identity) throw new Error("state snapshot omitted shell_identity");
      options.applyIdentity(snapshot.shell_identity);
      return true;
    },
  };
}
