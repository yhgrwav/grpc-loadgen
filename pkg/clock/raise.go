package clock

// timerAPI is the host's timer resolution calls; nil fields are calls the host
// does not have. Periods are in 100 ns units, as ntdll takes them.
type timerAPI struct {
	finest func() (uint32, error)
	set    func(period uint32, on bool) error
	begin  func(ms uint32) error
	end    func(ms uint32) error
}

// Raise asks the host for its finest timer period for the length of a run and
// returns what undoes it. raised is false when the host refused: the step can
// then coarsen mid-run where no measure sees it. A host with no timer to raise
// is raised.
func Raise() (restore func(), raised bool) {
	return raiseWith(hostTimer)
}

func raiseWith(api timerAPI) (restore func(), raised bool) {
	if api.finest == nil && api.begin == nil {
		return func() {}, true
	}
	if api.finest != nil && api.set != nil {
		if p, err := api.finest(); err == nil && api.set(p, true) == nil {
			return func() { _ = api.set(p, false) }, true
		}
	}
	if api.begin != nil && api.begin(1) == nil {
		return func() { _ = api.end(1) }, true
	}

	return func() {}, false
}
