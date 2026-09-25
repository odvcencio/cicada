package phrase

import "math/bits"

const pcgIncrement uint64 = 0xda3e39cb94b95bdb

type stream struct {
	state uint64
	trace []Draw
}

func newStream(seed uint64) stream {
	s := stream{state: seed + pcgIncrement}
	s.next()
	s.state += seed
	s.next()
	return s
}

func (s *stream) next() uint32 {
	old := s.state
	s.state = old*6364136223846793005 + pcgIncrement
	x := uint32(((old >> 18) ^ old) >> 27)
	return bits.RotateLeft32(x, -int(old>>59))
}

func (s *stream) choose(pass string, step, n int) int {
	if n < 1 {
		panic("phrase: choose requires a positive count")
	}
	raw := s.next()
	choice := int(raw % uint32(n))
	s.trace = append(s.trace, Draw{Pass: pass, Step: step, Raw: raw, Choice: choice})
	return choice
}

func (s *stream) pick(pass string, step int, weights []uint8) int {
	sum := 0
	for _, weight := range weights {
		sum += int(weight)
	}
	if sum == 0 {
		panic("phrase: pick requires positive weight")
	}
	raw := s.next()
	choice := int(raw % uint32(sum))
	for index, weight := range weights {
		if choice < int(weight) {
			s.trace = append(s.trace, Draw{Pass: pass, Step: step, Raw: raw, Choice: index})
			return index
		}
		choice -= int(weight)
	}
	panic("phrase: unreachable weighted choice")
}

func (s *stream) unit24(pass string, step int) uint32 {
	raw := s.next()
	choice := raw >> 8
	s.trace = append(s.trace, Draw{Pass: pass, Step: step, Raw: raw, Choice: int(choice)})
	return choice
}
